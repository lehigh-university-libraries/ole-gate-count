CREATE DATABASE `ole`;
USE `ole`;
CREATE TABLE `lib_gate_counts` (
  `timestamp` datetime DEFAULT NULL,
  `gate_name` varchar(64) CHARACTER SET utf8 COLLATE utf8_general_ci DEFAULT NULL,
  `alarm_count` int(11) DEFAULT NULL,
  `alarm_diff` int(11) DEFAULT NULL,
  `incoming_patrons_count` int(11) DEFAULT NULL,
  `incoming_diff` int(11) DEFAULT NULL,
  `outgoing_patrons_count` int(11) DEFAULT NULL,
  `outgoing_diff` int(11) DEFAULT NULL,
  KEY `lib_gate_time_idx` (`timestamp`),
  KEY `lib_gate_name_idx` (`gate_name`),
  KEY `lib_gate_time_name_idx` (`timestamp`,`gate_name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE USER `ole`@`%` IDENTIFIED BY 'CHANGEME';
GRANT ALL PRIVILEGES ON ole.* TO `ole`@`%`
