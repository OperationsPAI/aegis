-- SSO Extraction PR-1a: collapse 4 scoped-role tables into user_scoped_roles
-- and reshape the permissions table for the multi-service scope_type model.
-- Run once when shipping PR-1a. Destructive: depends on the no-prod-data
-- assumption captured in docs/sso-extraction-design.md §9.

DROP TABLE IF EXISTS user_projects;
DROP TABLE IF EXISTS user_teams;
DROP TABLE IF EXISTS user_containers;
DROP TABLE IF EXISTS user_datasets;

ALTER TABLE permissions DROP COLUMN IF EXISTS scope;
ALTER TABLE permissions DROP COLUMN IF EXISTS resource_id;
ALTER TABLE permissions ADD COLUMN service VARCHAR(64) NOT NULL DEFAULT 'aegis';
ALTER TABLE permissions ADD COLUMN scope_type VARCHAR(64) NOT NULL DEFAULT '';

-- AutoMigrate creates user_scoped_roles and user_project_workspaces on next boot.

TRUNCATE TABLE users;
TRUNCATE TABLE user_roles;
TRUNCATE TABLE api_keys;
