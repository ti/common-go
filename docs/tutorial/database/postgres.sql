create table audit_configs
(       "_id"           bigint unsigned auto_increment,
        "project"       CHAR(64)        not null,
        "id"            CHAR(64),
        "app_id"                CHAR(64),
        "biz_id"                CHAR(64),
        "text"          json,
        "image"         json,
        "audio"         json,
        "video"         json,
        "created_at"            datetime(6),
        KEY("project"),
        constraint audit_configs_pk primary key (_id)
);