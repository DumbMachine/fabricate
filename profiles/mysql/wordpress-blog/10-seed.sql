-- WordPress seed: 50 users, 200 posts, ~600 postmeta rows, 400
-- comments, a small taxonomy. Procedural so this file stays small.

DELIMITER //
CREATE PROCEDURE seed_wp()
BEGIN
  DECLARE i INT DEFAULT 1;

  -- users
  WHILE i <= 50 DO
    INSERT INTO wp_users (user_login, user_email, user_registered, display_name)
      VALUES (CONCAT('user', i),
              CONCAT('user', i, '@example.com'),
              NOW() - INTERVAL FLOOR(RAND() * 365) DAY,
              CONCAT('User ', i));
    SET i = i + 1;
  END WHILE;

  -- posts (200): mix of statuses + types
  SET i = 1;
  WHILE i <= 200 DO
    INSERT INTO wp_posts (post_author, post_date, post_content, post_title,
                          post_excerpt, post_status, post_name, post_type,
                          post_modified, guid)
      VALUES (
        1 + FLOOR(RAND() * 50),
        NOW() - INTERVAL FLOOR(RAND() * 365 * 24 * 3600) SECOND,
        CONCAT('<p>Post body for entry ', i, '. Lorem ipsum dolor sit amet, ',
               'consectetur adipiscing elit. Used as filler text.</p>'),
        CONCAT('Post Title #', i),
        CONCAT('Excerpt for post ', i),
        ELT(1 + FLOOR(RAND() * 6), 'publish','publish','publish','publish','draft','trash'),
        CONCAT('post-', i),
        ELT(1 + FLOOR(RAND() * 8), 'post','post','post','post','post','post','page','attachment'),
        NOW(),
        CONCAT('http://example.test/?p=', i)
      );
    SET i = i + 1;
  END WHILE;

  -- postmeta: ~3 rows per post on average
  SET i = 1;
  WHILE i <= 200 DO
    INSERT INTO wp_postmeta (post_id, meta_key, meta_value)
      VALUES (i, '_edit_lock', CONCAT(UNIX_TIMESTAMP(), ':', 1 + FLOOR(RAND() * 50))),
             (i, '_edit_last', CAST(1 + FLOOR(RAND() * 50) AS CHAR)),
             (i, 'featured',   IF(RAND() < 0.15, '1', '0'));
    IF RAND() < 0.3 THEN
      INSERT INTO wp_postmeta (post_id, meta_key, meta_value)
        VALUES (i, '_thumbnail_id', CAST(1 + FLOOR(RAND() * 200) AS CHAR));
    END IF;
    SET i = i + 1;
  END WHILE;

  -- comments
  SET i = 1;
  WHILE i <= 400 DO
    INSERT INTO wp_comments (comment_post_ID, comment_author, comment_author_email,
                             comment_date, comment_content, comment_approved)
      VALUES (1 + FLOOR(RAND() * 200),
              CONCAT('Commenter ', i),
              CONCAT('c', i, '@example.com'),
              NOW() - INTERVAL FLOOR(RAND() * 90 * 24 * 3600) SECOND,
              ELT(1 + FLOOR(RAND() * 4),
                'Great post — thanks!',
                'I disagree, see my blog.',
                '+1 to all of this',
                'spam-looking link removed'),
              ELT(1 + FLOOR(RAND() * 4), '1','1','1','spam'));
    SET i = i + 1;
  END WHILE;
END //
DELIMITER ;

CALL seed_wp();
DROP PROCEDURE seed_wp;

-- Taxonomy: 5 categories + 10 tags, attached to ~half the posts.
INSERT INTO wp_terms (term_id, name, slug) VALUES
  (1,'News','news'), (2,'Engineering','engineering'),
  (3,'Product','product'), (4,'Community','community'),
  (5,'Uncategorized','uncategorized'),
  (6,'mysql','mysql'), (7,'postgres','postgres'),
  (8,'caching','caching'), (9,'observability','observability'),
  (10,'security','security'), (11,'devex','devex'),
  (12,'launch','launch'), (13,'rfc','rfc'),
  (14,'oncall','oncall'), (15,'postmortem','postmortem');

INSERT INTO wp_term_taxonomy (term_taxonomy_id, term_id, taxonomy, description) VALUES
  (1, 1, 'category', ''), (2, 2, 'category', ''), (3, 3, 'category', ''),
  (4, 4, 'category', ''), (5, 5, 'category', ''),
  (6, 6, 'post_tag', ''), (7, 7, 'post_tag', ''), (8, 8, 'post_tag', ''),
  (9, 9, 'post_tag', ''), (10, 10, 'post_tag', ''), (11, 11, 'post_tag', ''),
  (12, 12, 'post_tag', ''), (13, 13, 'post_tag', ''), (14, 14, 'post_tag', ''),
  (15, 15, 'post_tag', '');

-- Random taxonomy attachments. Every post gets exactly one category
-- (1–5) and 0–3 tags (6–15).
INSERT INTO wp_term_relationships (object_id, term_taxonomy_id)
SELECT ID, 1 + FLOOR(RAND() * 5) FROM wp_posts WHERE post_type = 'post';

INSERT IGNORE INTO wp_term_relationships (object_id, term_taxonomy_id)
SELECT ID, 6 + FLOOR(RAND() * 10) FROM wp_posts WHERE post_type = 'post' AND RAND() < 0.6;

INSERT IGNORE INTO wp_term_relationships (object_id, term_taxonomy_id)
SELECT ID, 6 + FLOOR(RAND() * 10) FROM wp_posts WHERE post_type = 'post' AND RAND() < 0.3;
