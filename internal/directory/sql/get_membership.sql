SELECT m.list_id, m.user_id, l.address, u.address, u.display_name, m.created_at
FROM distribution_list_members m
JOIN distribution_lists l ON l.id = m.list_id
JOIN users u ON u.id = m.user_id
WHERE m.list_id = $1 AND m.user_id = $2
