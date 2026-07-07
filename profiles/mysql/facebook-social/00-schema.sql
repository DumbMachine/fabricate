CREATE TABLE users (
  id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name        VARCHAR(120) NOT NULL,
  email       VARCHAR(255) NOT NULL UNIQUE,
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Symmetric edges: for each accepted friendship we insert (a,b) AND
-- (b,a). Doubles the row count but makes both directions of the
-- friend-list query a simple index seek.
CREATE TABLE friendships (
  user_id     BIGINT NOT NULL,
  friend_id   BIGINT NOT NULL,
  status      ENUM('pending','accepted','blocked') NOT NULL DEFAULT 'accepted',
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, friend_id),
  INDEX idx_friend_status (user_id, status)
);

CREATE TABLE posts (
  id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  author_id   BIGINT NOT NULL,
  body        TEXT NOT NULL,
  visibility  ENUM('public','friends','only_me') NOT NULL DEFAULT 'friends',
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_posts_author_created (author_id, created_at)
);

CREATE TABLE likes (
  user_id    BIGINT NOT NULL,
  post_id    BIGINT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, post_id),
  INDEX idx_likes_post (post_id)
);

CREATE TABLE comments (
  id         BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  post_id    BIGINT NOT NULL,
  author_id  BIGINT NOT NULL,
  body       TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_comments_post (post_id, created_at)
);
