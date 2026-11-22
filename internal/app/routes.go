package app

// registerRoutes wires every feature package. Only the integrator edits this
// file: feature tracks expose New(Deps) and Register(*web.Router), and the
// wiring is added here at merge time.
func registerRoutes(a *App) error {
	registerCore(a)
	return nil
}
