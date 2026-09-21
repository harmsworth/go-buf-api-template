你是 Go 语言微服务工程专家。我们的项目使用 `buf` 生成传输层 DTO（如 userv1.User），使用 `sqlc` 生成持久层代码（如 db.User）。

请帮我实现 Service 层方法或转换函数。要求：
1. 结构体转换（Converter）：
   - 编写纯原生、零反射的转换函数（如 dbUserToProto(u db.User) *userv1.User）。
   - 正确处理时间映射：使用 timestamppb.New(u.CreatedAt)。
   - 正确处理 sql.NullString / sql.NullInt64 等 DB 可空字段到 Proto 指针或基础类型的转换。
   - 绝对严禁将 password_hash 等数据库敏感字段映射到 Proto 结构体中。
2. 业务处理（Service）：
   - 接收 req *userv1.CreateUserRequest，调用 sqlc 的 db.Queries 链式方法。
   - 保持代码极简、类型安全，严禁引入 GORM 或反射依赖。
