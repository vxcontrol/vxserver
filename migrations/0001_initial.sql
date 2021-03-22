-- +migrate Up

CREATE TABLE IF NOT EXISTS `agents` (
  `id` INT(10) UNSIGNED NOT NULL AUTO_INCREMENT,
  `hash` VARCHAR(32) NOT NULL,
  `ip` VARCHAR(50) NOT NULL,
  `description` VARCHAR(255) NOT NULL,
  `info` JSON NOT NULL,
  `status` ENUM('created','connected','disconnected','removed') NOT NULL,
  `os_type` VARCHAR(30) GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.os.type'))) STORED,
  `os_arch` VARCHAR(10) GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.os.arch'))) STORED,
  `os_name` VARCHAR(30) GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.os.name'))) STORED,
  `connected_date` DATETIME DEFAULT NULL,
  `created_date` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `hash` (`hash`)
) ENGINE=InnoDB AUTO_INCREMENT=0 DEFAULT CHARSET=utf8;

CREATE TABLE IF NOT EXISTS `events` (
  `id` INT(10) UNSIGNED NOT NULL AUTO_INCREMENT,
  `module_id` INT(10) UNSIGNED NOT NULL,
  `agent_id` INT(10) UNSIGNED NOT NULL,
  `info` JSON NOT NULL,
  `name` VARCHAR(50) GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.name'))) STORED,
  `data` JSON GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.data'))) VIRTUAL,
  `data_text` TEXT GENERATED ALWAYS AS (json_unquote(json_extract(`info`,'$.data'))) STORED,
  `uniq` VARCHAR(255) GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.uniq'))) STORED,
  `date` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `module_and_agent` (`module_id`,`agent_id`),
  KEY `module_id` (`module_id`),
  KEY `agent_id` (`agent_id`),
  KEY `name` (`name`),
  KEY `uniq` (`uniq`),
  FULLTEXT KEY `data_text` (`data_text`)
) ENGINE=InnoDB AUTO_INCREMENT=0 DEFAULT CHARSET=utf8;

CREATE TABLE IF NOT EXISTS `modules` (
  `id` INT(10) UNSIGNED NOT NULL AUTO_INCREMENT,
  `agent_id` INT(10) UNSIGNED NOT NULL,
  `status` ENUM('joined','inactive') NOT NULL,
  `join_date` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `config_schema` JSON NOT NULL,
  `default_config` JSON NOT NULL,
  `current_config` JSON NOT NULL,
  `event_data_schema` JSON DEFAULT NULL,
  `event_config_schema` JSON DEFAULT NULL,
  `default_event_config` JSON DEFAULT NULL,
  `current_event_config` JSON DEFAULT NULL,
  `changelog` JSON NOT NULL,
  `locale` JSON NOT NULL,
  `info` JSON NOT NULL,
  `name` VARCHAR(50) GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.name'))) STORED,
  `version` VARCHAR(10) GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.version'))) STORED,
  `tags` JSON GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.tags'))) VIRTUAL,
  `events` JSON GENERATED ALWAYS AS (json_unquote(json_extract(`info`,_utf8mb4'$.events'))) VIRTUAL,
  `system` TINYINT(1) GENERATED ALWAYS AS (json_extract(`info`,_utf8mb4'$.system')) STORED,
  `last_update` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `UNIQUE` (`agent_id`,`name`),
  KEY `name` (`name`)
) ENGINE=InnoDB AUTO_INCREMENT=0 DEFAULT CHARSET=utf8;

-- +migrate Down

DROP TABLE `agents`;
DROP TABLE `events`;
DROP TABLE `modules`;
