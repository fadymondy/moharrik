-- Postgres queries for invitations (tenant predicate required).

-- name: ListPendingInvitations :many
SELECT * FROM invitations
WHERE workspace_id = $1 AND accepted_at IS NULL
ORDER BY created_at DESC;

-- name: DeletePendingInvitation :exec
DELETE FROM invitations WHERE workspace_id = $1 AND id = $2 AND accepted_at IS NULL;
