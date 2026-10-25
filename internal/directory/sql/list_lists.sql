SELECT l.id, l.address, l.display_name, l.created_at, l.updated_at,
       (SELECT count(*) FROM distribution_list_members m WHERE m.list_id = l.id)
FROM distribution_lists l
WHERE ($1::bigint = 0 OR l.id > $1)
ORDER BY l.id
LIMIT $2
