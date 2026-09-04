package agent

import "sync"

type fileReads struct {
	mu     sync.Mutex
	hashes map[string]string
}

type scopedFileReads struct {
	session *session
	local   *fileReads
}

func (r *scopedFileReads) Record(key, hash string) {
	r.local.Record(key, hash)
}

func (r *scopedFileReads) Hash(key string) (string, bool) {
	return r.local.Hash(key)
}

func (r *scopedFileReads) Clear() {
	r.session.clearFileReads()
}

func (s *session) primaryFileReads() FileReads {
	return &scopedFileReads{session: s, local: &s.reads}
}

func (s *session) childFileReads() (FileReads, func()) {
	local := &fileReads{}
	s.readScopesMu.Lock()
	if s.readScopes == nil {
		s.readScopes = make(map[*fileReads]struct{})
	}
	s.readScopes[local] = struct{}{}
	s.readScopesMu.Unlock()
	return &scopedFileReads{session: s, local: local}, func() {
		s.readScopesMu.Lock()
		delete(s.readScopes, local)
		s.readScopesMu.Unlock()
	}
}

func (s *session) clearFileReads() {
	s.readScopesMu.Lock()
	scopes := make([]*fileReads, 0, len(s.readScopes)+1)
	scopes = append(scopes, &s.reads)
	for scope := range s.readScopes {
		scopes = append(scopes, scope)
	}
	s.readScopesMu.Unlock()
	for _, scope := range scopes {
		scope.Clear()
	}
}

func (r *fileReads) Record(key, hash string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hashes == nil {
		r.hashes = make(map[string]string)
	}
	r.hashes[key] = hash
}

func (r *fileReads) Hash(key string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	hash, ok := r.hashes[key]
	return hash, ok
}

func (r *fileReads) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hashes = nil
}
