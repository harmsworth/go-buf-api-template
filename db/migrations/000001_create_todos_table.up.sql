CREATE TABLE IF NOT EXISTS `todos` (
  `id`           VARCHAR(36)   NOT NULL COMMENT '全局唯一 ID（UUID v4），对应 todo.v1.Todo.id',
  `title`        VARCHAR(128)  NOT NULL COMMENT '标题，对应 todo.v1.Todo.title',
  `description`  VARCHAR(2048) DEFAULT NULL COMMENT '描述，NULL=未设置，对应 todo.v1.Todo.description',
  `status`       TINYINT       NOT NULL DEFAULT 1 COMMENT '状态：0=未指定 1=待处理 2=进行中 3=已完成',
  `created_at`   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) COMMENT '创建时间',
  `updated_at`   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3) COMMENT '更新时间',
  `completed_at` DATETIME(3)   NULL DEFAULT NULL COMMENT '完成时间，NULL=未完成',
  `deleted_at`   DATETIME(3)   NULL DEFAULT NULL COMMENT '软删除时间，NULL=未删除',
  PRIMARY KEY (`id`),
  KEY `idx_todos_status` (`status`),
  KEY `idx_todos_created_at` (`created_at`),
  KEY `idx_todos_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='待办事项表，对应 todo.v1.Todo';
