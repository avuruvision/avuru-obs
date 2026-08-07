-- SSO group memberships for OIDC users, captured at each login. The group→grant
-- mapping (auth.oidc.mapping, hot-reloadable) is applied in-memory at identity
-- resolution, so manual grants in auth_grant stay independent and a mapping edit
-- takes effect on the next request. Local users keep this empty.
ALTER TABLE {db}.auth_user
    ADD COLUMN IF NOT EXISTS `OidcGroups` Array(String) DEFAULT [];
