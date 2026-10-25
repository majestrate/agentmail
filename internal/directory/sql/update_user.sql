UPDATE users SET
    address = CASE WHEN $2::bool THEN $3 ELSE address END,
    display_name = CASE WHEN $4::bool THEN $5 ELSE display_name END,
    password_hash = CASE WHEN $6::bool THEN $7 ELSE password_hash END
WHERE id = $1
