-- Local development roles, mirroring the cluster:
--   ecogo_owner  owns the schema, runs migrations; BYPASSRLS so the SECURITY DEFINER lookups work
--   ecogo_app    used by api, ingest and worker; subject to row-level security
CREATE ROLE ecogo_owner LOGIN PASSWORD 'ecogo_owner' BYPASSRLS;
CREATE ROLE ecogo_app LOGIN PASSWORD 'ecogo_app';
CREATE DATABASE ecogo OWNER ecogo_owner;
