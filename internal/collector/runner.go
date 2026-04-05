package collector

// CommandRunner executes a named binary with the given arguments and returns its stdout.
// Injecting a fake implementation in tests avoids any dependency on real system tools.
type CommandRunner func(name string, args ...string) ([]byte, error)
