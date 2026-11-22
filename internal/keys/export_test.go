package keys

// WithChecksum appends a valid checksum to "snap_<id>_<secret>", so tests can
// build well-formed keys that do not exist.
func WithChecksum(body string) string { return body + "_" + checksum(body) }
