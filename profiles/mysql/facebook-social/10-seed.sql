-- Procedural seed: 1000 users, ~8000 mutual friendships, 3000 posts,
-- 25000 likes, 5000 comments. Generated in stored procedures so the
-- file stays tiny while the row counts stay realistic.

DELIMITER //
CREATE PROCEDURE seed_social()
BEGIN
  DECLARE i INT DEFAULT 1;
  DECLARE u1 INT;
  DECLARE u2 INT;

  -- users
  WHILE i <= 1000 DO
    INSERT INTO users (name, email)
      VALUES (CONCAT('User ', i), CONCAT('user', i, '@example.com'));
    SET i = i + 1;
  END WHILE;

  -- friendships: pick 4000 unique unordered pairs (u1<u2) and
  -- insert both directions. Density ≈ 8 friends per user, similar
  -- to what a small launch cohort looks like.
  SET i = 0;
  WHILE i < 4000 DO
    SET u1 = 1 + FLOOR(RAND() * 1000);
    SET u2 = 1 + FLOOR(RAND() * 1000);
    IF u1 < u2 THEN
      INSERT IGNORE INTO friendships (user_id, friend_id) VALUES (u1, u2);
      INSERT IGNORE INTO friendships (user_id, friend_id) VALUES (u2, u1);
      SET i = i + 1;
    END IF;
  END WHILE;

  -- posts
  SET i = 1;
  WHILE i <= 3000 DO
    INSERT INTO posts (author_id, body, visibility, created_at)
      VALUES (
        1 + FLOOR(RAND() * 1000),
        CONCAT('Post #', i, ' — ', ELT(1 + FLOOR(RAND() * 4),
          'just shipped a thing',
          'coffee was excellent today',
          'dog photo incoming',
          'mild rant about a meeting that should have been an email')),
        ELT(1 + FLOOR(RAND() * 3), 'public', 'friends', 'only_me'),
        NOW() - INTERVAL FLOOR(RAND() * 30 * 24 * 3600) SECOND
      );
    SET i = i + 1;
  END WHILE;

  -- likes
  SET i = 0;
  WHILE i < 25000 DO
    INSERT IGNORE INTO likes (user_id, post_id)
      VALUES (1 + FLOOR(RAND() * 1000),
              1 + FLOOR(RAND() * 3000));
    SET i = i + 1;
  END WHILE;

  -- comments
  SET i = 1;
  WHILE i <= 5000 DO
    INSERT INTO comments (post_id, author_id, body)
      VALUES (1 + FLOOR(RAND() * 3000),
              1 + FLOOR(RAND() * 1000),
              ELT(1 + FLOOR(RAND() * 4),
                'lol', '+1', 'this is the way', 'disagree, see DM'));
    SET i = i + 1;
  END WHILE;
END //
DELIMITER ;

CALL seed_social();
DROP PROCEDURE seed_social;
