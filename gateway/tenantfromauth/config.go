package tenantfromauth

// Config is the tenantfromauth processor configuration. It is intentionally
// empty: the processor's whole behavior is "stamp avuru.tenant from the
// validated ingest-key project when present, else pass through". The mode gate
// lives in the avuruingestauth extension (it only attaches the project auth
// attribute in enforce mode), so this processor stays a dumb, always-on
// stamper — in log/off mode there is simply no attribute to act on.
type Config struct{}

func (c *Config) Validate() error { return nil }
