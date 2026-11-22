package items

// DropUploadState forgets in-memory upload state, as a restart would.
func (s *Service) DropUploadState() {
	s.upMu.Lock()
	s.ups = map[string]*upState{}
	s.upMu.Unlock()
}
