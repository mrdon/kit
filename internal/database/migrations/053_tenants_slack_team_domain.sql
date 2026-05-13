-- +goose Up

-- Slack workspace subdomain (e.g. "monarchbands" for monarchbands.slack.com).
-- Used to build https://<domain>.slack.com/openid/connect/authorize URLs so
-- the "Sign in with Slack" flow pins the workspace before the user reaches
-- Slack's sign-in page. Without it Slack falls back to a generic sign-in
-- that asks the user to type their workspace slug from memory.
ALTER TABLE tenants ADD COLUMN slack_team_domain TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE tenants DROP COLUMN slack_team_domain;
