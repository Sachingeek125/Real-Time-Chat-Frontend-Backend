-- Write your migrate up statements here
CREATE TABLE users(
user_id serial primary key,
user_name text
);
---- create above / drop below ----

-- Write your migrate down statements here. If this migration is irreversible
-- Then delete the separator line above.
