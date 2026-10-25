UPDATE distribution_lists SET
    address = CASE WHEN $2::bool THEN $3 ELSE address END,
    display_name = CASE WHEN $4::bool THEN $5 ELSE display_name END
WHERE id = $1
