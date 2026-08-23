package plugin

import "errors"

// asAPIError is errors.As with the plugin's own type, kept in one place so the
// status mapping reads as intent rather than as plumbing.
func asAPIError(err error, target **apiError) bool { return errors.As(err, target) }
