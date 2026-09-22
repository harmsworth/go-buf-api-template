// Package aipgorm 把 go.einride.tech/aip 解析出的 AIP 过滤/排序表达式翻译成 GORM 查询。
//
// 职责边界：只做"表达式 → SQL 片段"的翻译，不含任何业务语义；
// 各业务模块声明自己的 Schema（proto 字段 → 数据库列），再调用 ApplyFilter / ApplyOrderBy。
package aipgorm

import (
	"fmt"
	"sort"
	"strings"

	"go.einride.tech/aip/filtering"
	"go.einride.tech/aip/ordering"

	expr "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"gorm.io/gorm"
)

// Field 描述一个可过滤 / 可排序的字段：proto 字段路径 → 数据库列名。
type Field struct {
	// Column 是数据库列名（来自 Schema，非用户输入，因此拼接 SQL 是安全的）。
	Column string
	// Type 是过滤表达式类型检查用的类型（filtering.TypeString / TypeInt / TypeBool / TypeTimestamp）。
	Type *expr.Type
	// Enum 非空表示该字段是 proto 枚举，用于把 "DONE" 翻译成枚举号再与整型列比较。
	Enum protoreflect.EnumType
}

// Schema 是 proto 字段路径到 Field 的映射。
type Schema map[string]Field

// Declarations 依据 Schema 构造过滤表达式的类型声明（供 ParseFilter 做类型检查）。
// 未声明的字段会在 ParseFilter 阶段就被拒绝，不会走到 SQL 翻译。
//
// 注意：一律按 Field.Type 声明。枚举字段请声明为 TypeString（写 status = "DONE"），
// 因为 einride 的 '=' 没有 enum↔string 重载；Field.Enum 仅用于把枚举名翻译成枚举号。
func Declarations(schema Schema) (*filtering.Declarations, error) {
	opts := make([]filtering.DeclarationOption, 0, len(schema)+1)
	opts = append(opts, filtering.DeclareStandardFunctions())
	for name, f := range schema {
		opts = append(opts, filtering.DeclareIdent(name, f.Type))
	}
	return filtering.NewDeclarations(opts...)
}

// Paths 返回 Schema 中所有可过滤 / 可排序的字段路径（供 ordering 白名单校验）。
func Paths(schema Schema) []string {
	paths := make([]string, 0, len(schema))
	for name := range schema {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}

// ApplyFilter 解析并翻译 req 中的 AIP-160 filter 表达式，返回带 WHERE 条件的 *gorm.DB。
func ApplyFilter(db *gorm.DB, req filtering.Request, decls *filtering.Declarations, schema Schema) (*gorm.DB, error) {
	if req.GetFilter() == "" {
		return db, nil
	}
	filter, err := filtering.ParseFilter(req, decls)
	if err != nil {
		return nil, fmt.Errorf("parse filter: %w", err)
	}
	cond, err := translate(filter.CheckedExpr.GetExpr(), schema)
	if err != nil {
		return nil, err
	}
	return db.Where(cond.sql, cond.args...), nil
}

// ApplyOrderBy 解析并翻译 req 中的 AIP-132 order_by 表达式；为空时使用 fallback。
func ApplyOrderBy(db *gorm.DB, req ordering.Request, schema Schema, fallback string) (*gorm.DB, error) {
	if req.GetOrderBy() == "" {
		return db.Order(fallback), nil
	}
	orderBy, err := ordering.ParseOrderBy(req)
	if err != nil {
		return nil, fmt.Errorf("parse order_by: %w", err)
	}
	// 白名单校验：只允许按 Schema 中声明的字段排序。
	if err := orderBy.ValidateForPaths(Paths(schema)...); err != nil {
		return nil, fmt.Errorf("validate order_by: %w", err)
	}
	parts := make([]string, 0, len(orderBy.Fields))
	for _, f := range orderBy.Fields {
		field, ok := schema[f.Path]
		if !ok {
			return nil, fmt.Errorf("order_by field %q is not supported", f.Path)
		}
		if f.Desc {
			parts = append(parts, field.Column+" DESC")
		} else {
			parts = append(parts, field.Column+" ASC")
		}
	}
	return db.Order(strings.Join(parts, ", ")), nil
}

// cond 是一条 SQL 条件片段及其参数。
type cond struct {
	sql  string
	args []any
}

// translate 递归把 CEL 表达式翻译成 SQL 条件。
func translate(e *expr.Expr, schema Schema) (cond, error) {
	switch kind := e.GetExprKind().(type) {
	case *expr.Expr_CallExpr:
		return translateCall(kind.CallExpr, schema)
	case *expr.Expr_IdentExpr:
		// 形如 `show_deleted` 的裸布尔字段：等价于 = true。
		field, ok := schema[kind.IdentExpr.GetName()]
		if !ok {
			return cond{}, fmt.Errorf("filter field %q is not supported", kind.IdentExpr.GetName())
		}
		return cond{sql: field.Column + " = ?", args: []any{true}}, nil
	default:
		return cond{}, fmt.Errorf("unsupported filter expression kind %T", kind)
	}
}

func translateCall(call *expr.Expr_Call, schema Schema) (cond, error) {
	switch call.GetFunction() {
	case filtering.FunctionAnd:
		return joinPredicates(call.GetArgs(), schema, " AND ")
	case filtering.FunctionOr:
		return joinPredicates(call.GetArgs(), schema, " OR ")
	case filtering.FunctionNot:
		inner, err := translate(call.GetArgs()[0], schema)
		if err != nil {
			return cond{}, err
		}
		return cond{sql: "NOT (" + inner.sql + ")", args: inner.args}, nil
	case filtering.FunctionEquals,
		filtering.FunctionNotEquals,
		filtering.FunctionLessThan,
		filtering.FunctionLessEquals,
		filtering.FunctionGreaterThan,
		filtering.FunctionGreaterEquals,
		filtering.FunctionHas:
		return translateComparison(call, schema)
	default:
		return cond{}, fmt.Errorf("unsupported filter function %q", call.GetFunction())
	}
}

func joinPredicates(args []*expr.Expr, schema Schema, op string) (cond, error) {
	parts := make([]string, 0, len(args))
	var allArgs []any
	for _, arg := range args {
		c, err := translate(arg, schema)
		if err != nil {
			return cond{}, err
		}
		parts = append(parts, c.sql)
		allArgs = append(allArgs, c.args...)
	}
	return cond{sql: "(" + strings.Join(parts, op) + ")", args: allArgs}, nil
}

func translateComparison(call *expr.Expr_Call, schema Schema) (cond, error) {
	if len(call.GetArgs()) != 2 {
		return cond{}, fmt.Errorf("comparison %q requires exactly 2 arguments", call.GetFunction())
	}

	// 左侧必须是字段标识。
	ident := call.GetArgs()[0].GetIdentExpr()
	if ident == nil {
		return cond{}, fmt.Errorf("left-hand side of %q must be a field", call.GetFunction())
	}
	field, ok := schema[ident.GetName()]
	if !ok {
		return cond{}, fmt.Errorf("filter field %q is not supported", ident.GetName())
	}

	value, err := constValue(call.GetArgs()[1], field)
	if err != nil {
		return cond{}, err
	}

	switch call.GetFunction() {
	case filtering.FunctionEquals:
		if value == nil {
			return cond{sql: field.Column + " IS NULL"}, nil
		}
		return cond{sql: field.Column + " = ?", args: []any{value}}, nil
	case filtering.FunctionNotEquals:
		if value == nil {
			return cond{sql: field.Column + " IS NOT NULL"}, nil
		}
		return cond{sql: field.Column + " <> ?", args: []any{value}}, nil
	case filtering.FunctionLessThan:
		return cond{sql: field.Column + " < ?", args: []any{value}}, nil
	case filtering.FunctionLessEquals:
		return cond{sql: field.Column + " <= ?", args: []any{value}}, nil
	case filtering.FunctionGreaterThan:
		return cond{sql: field.Column + " > ?", args: []any{value}}, nil
	case filtering.FunctionGreaterEquals:
		return cond{sql: field.Column + " >= ?", args: []any{value}}, nil
	case filtering.FunctionHas:
		// AIP-160 的 `:` 运算符：字符串子串匹配，映射到 LIKE %value%。
		s, ok := value.(string)
		if !ok {
			return cond{}, fmt.Errorf("operator %q only supports string values", call.GetFunction())
		}
		return cond{sql: field.Column + " LIKE ?", args: []any{"%" + s + "%"}}, nil
	default:
		return cond{}, fmt.Errorf("unsupported filter function %q", call.GetFunction())
	}
}

// constValue 提取比较右侧的字面量，并按字段类型做归一化（枚举名 → 枚举号）。
func constValue(e *expr.Expr, field Field) (any, error) {
	// timestamp("...") / duration("...") 等函数调用：取其字符串实参原样传给 MySQL。
	if call := e.GetCallExpr(); call != nil {
		if len(call.GetArgs()) == 1 {
			if c := call.GetArgs()[0].GetConstExpr(); c != nil {
				return c.GetStringValue(), nil
			}
		}
		return nil, fmt.Errorf("unsupported function %q in filter value", call.GetFunction())
	}

	// 枚举常量可能以标识形式出现（如 USER_STATUS_ACTIVE）。
	if ident := e.GetIdentExpr(); ident != nil {
		if field.Enum == nil {
			return nil, fmt.Errorf("field does not support enum value %q", ident.GetName())
		}
		num, ok := enumNumber(field.Enum, ident.GetName())
		if !ok {
			return nil, fmt.Errorf("unknown enum value %q", ident.GetName())
		}
		return num, nil
	}

	c := e.GetConstExpr()
	if c == nil {
		return nil, fmt.Errorf("unsupported filter value expression %T", e.GetExprKind())
	}

	switch v := c.GetConstantKind().(type) {
	case *expr.Constant_StringValue:
		// 枚举列收到字符串时，按枚举名解析（支持 "DONE" 与 "TODO_STATUS_DONE" 两种写法）。
		if field.Enum != nil {
			num, ok := enumNumber(field.Enum, v.StringValue)
			if !ok {
				return nil, fmt.Errorf("unknown enum value %q", v.StringValue)
			}
			return num, nil
		}
		return v.StringValue, nil
	case *expr.Constant_Int64Value:
		return v.Int64Value, nil
	case *expr.Constant_Uint64Value:
		return v.Uint64Value, nil
	case *expr.Constant_DoubleValue:
		return v.DoubleValue, nil
	case *expr.Constant_BoolValue:
		return v.BoolValue, nil
	case *expr.Constant_TimestampValue:
		// 直接交给 MySQL 与 DATETIME(3) 列比较。
		return v.TimestampValue.AsTime(), nil
	case *expr.Constant_NullValue:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported filter constant %T", v)
	}
}

// enumNumber 按名称解析枚举值，兼容短名（DONE）与全限定名（TODO_STATUS_DONE）。
func enumNumber(et protoreflect.EnumType, name string) (int32, bool) {
	desc := et.Descriptor()
	if v := desc.Values().ByName(protoreflect.Name(name)); v != nil {
		return int32(v.Number()), true
	}
	// 用首值的公共前缀补齐短名：TODO_STATUS_UNSPECIFIED → TODO_STATUS_
	if n := desc.Values().Len(); n > 0 {
		first := string(desc.Values().Get(0).Name())
		if idx := strings.LastIndex(first, "_"); idx > 0 {
			if v := desc.Values().ByName(protoreflect.Name(first[:idx+1] + name)); v != nil {
				return int32(v.Number()), true
			}
		}
	}
	return 0, false
}
