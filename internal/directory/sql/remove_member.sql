DELETE FROM distribution_list_members
WHERE list_id = $1 AND user_id = $2
