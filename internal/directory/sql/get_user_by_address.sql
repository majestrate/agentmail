SELECT u.id, u.address, u.display_name, u.created_at, u.updated_at,
       (SELECT count(*) FROM distribution_list_members m WHERE m.user_id = u.id)
FROM users u
WHERE u.address = $1
