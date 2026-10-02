-- Contacts list, newest first, with keyset paging.
CREATE INDEX contacts_created_idx ON contacts (tenant_id, created_at DESC, id DESC);
