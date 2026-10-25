INSERT INTO distribution_lists (address, display_name)
VALUES ($1, $2)
RETURNING id
