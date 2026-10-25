SELECT l.id, l.address, l.display_name, l.created_at, l.updated_at,
       (SELECT count(*) FROM distribution_list_members m WHERE m.list_id = l.id)
FROM distribution_lists l
WHERE l.address = $1
