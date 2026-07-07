-- A faithful-enough subset of the WordPress 6.x schema. Column
-- names and types match wp-includes/schema.php so an agent's
-- muscle memory works.

CREATE TABLE wp_users (
  ID                   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_login           VARCHAR(60) NOT NULL DEFAULT '',
  user_email           VARCHAR(100) NOT NULL DEFAULT '',
  user_registered      DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00',
  display_name         VARCHAR(250) NOT NULL DEFAULT '',
  PRIMARY KEY (ID),
  UNIQUE KEY user_login_key (user_login)
);

CREATE TABLE wp_posts (
  ID                   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  post_author          BIGINT UNSIGNED NOT NULL DEFAULT 0,
  post_date            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  post_content         LONGTEXT NOT NULL,
  post_title           TEXT NOT NULL,
  post_excerpt         TEXT NOT NULL,
  post_status          VARCHAR(20) NOT NULL DEFAULT 'publish',
  comment_status       VARCHAR(20) NOT NULL DEFAULT 'open',
  post_password        VARCHAR(255) NOT NULL DEFAULT '',
  post_name            VARCHAR(200) NOT NULL DEFAULT '',
  post_modified        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  post_parent          BIGINT UNSIGNED NOT NULL DEFAULT 0,
  guid                 VARCHAR(255) NOT NULL DEFAULT '',
  menu_order           INT NOT NULL DEFAULT 0,
  post_type            VARCHAR(20) NOT NULL DEFAULT 'post',
  comment_count        BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (ID),
  KEY post_name (post_name(191)),
  KEY type_status_date (post_type, post_status, post_date, ID),
  KEY post_parent (post_parent),
  KEY post_author (post_author)
);

CREATE TABLE wp_postmeta (
  meta_id      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  post_id      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  meta_key     VARCHAR(255) DEFAULT NULL,
  meta_value   LONGTEXT,
  PRIMARY KEY (meta_id),
  KEY post_id (post_id),
  KEY meta_key (meta_key(191))
);

CREATE TABLE wp_comments (
  comment_ID            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  comment_post_ID       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  comment_author        TINYTEXT NOT NULL,
  comment_author_email  VARCHAR(100) NOT NULL DEFAULT '',
  comment_date          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  comment_content       TEXT NOT NULL,
  comment_approved      VARCHAR(20) NOT NULL DEFAULT '1',
  PRIMARY KEY (comment_ID),
  KEY comment_post_ID (comment_post_ID),
  KEY comment_approved_date_gmt (comment_approved, comment_date)
);

CREATE TABLE wp_terms (
  term_id    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name       VARCHAR(200) NOT NULL DEFAULT '',
  slug       VARCHAR(200) NOT NULL DEFAULT '',
  PRIMARY KEY (term_id),
  KEY slug (slug(191))
);

CREATE TABLE wp_term_taxonomy (
  term_taxonomy_id  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  term_id           BIGINT UNSIGNED NOT NULL DEFAULT 0,
  taxonomy          VARCHAR(32) NOT NULL DEFAULT '',
  description       LONGTEXT NOT NULL,
  parent            BIGINT UNSIGNED NOT NULL DEFAULT 0,
  count             BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (term_taxonomy_id),
  UNIQUE KEY term_id_taxonomy (term_id, taxonomy),
  KEY taxonomy (taxonomy)
);

CREATE TABLE wp_term_relationships (
  object_id         BIGINT UNSIGNED NOT NULL DEFAULT 0,
  term_taxonomy_id  BIGINT UNSIGNED NOT NULL DEFAULT 0,
  term_order        INT NOT NULL DEFAULT 0,
  PRIMARY KEY (object_id, term_taxonomy_id),
  KEY term_taxonomy_id (term_taxonomy_id)
);
