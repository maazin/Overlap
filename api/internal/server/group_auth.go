package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/maazin/Overlap/api/internal/store"
)

// GroupTokenHeader carries a group member's token. Reusing the participant
// token's header name would be fine in principle -- the two never collide,
// since each is only ever checked against the URL namespace it arrived on --
// but a distinct name makes a stray request immediately obvious in a log.
const GroupTokenHeader = "X-Member-Token"

type groupCtxKey int

const (
	groupMemberKey groupCtxKey = iota
	groupKey
)

// authedMember is the pair every group-token-guarded handler needs.
type authedMember struct {
	group  store.Group
	member store.GroupMember
}

// requireGroupMember resolves the group from the path and the caller from the
// member token header, the same scoping discipline requireParticipant uses:
// the token is looked up within the group, so a token from a different group
// is simply a row that does not exist rather than a comparison to remember.
func (s *Server) requireGroupMember(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.Header.Get(GroupTokenHeader))
		if token == "" {
			s.writeError(w, r, http.StatusUnauthorized, "missing "+GroupTokenHeader)
			return
		}

		g, err := s.store.GroupBySlug(r.Context(), r.PathValue("slug"))
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusNotFound, "no such group")
			return
		}
		if err != nil {
			s.log.ErrorContext(r.Context(), "auth: load group", "err", err)
			s.writeError(w, r, http.StatusInternalServerError, "could not load group")
			return
		}

		m, err := s.store.GroupMemberByToken(r.Context(), g.ID, token)
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusUnauthorized, "unrecognised member token")
			return
		}
		if err != nil {
			s.log.ErrorContext(r.Context(), "auth: load group member", "err", err)
			s.writeError(w, r, http.StatusInternalServerError, "could not verify token")
			return
		}

		ctx := context.WithValue(r.Context(), groupKey, g)
		ctx = context.WithValue(ctx, groupMemberKey, m)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func mustAuthedMember(r *http.Request) authedMember {
	g, ok := r.Context().Value(groupKey).(store.Group)
	if !ok {
		panic("handler requires requireGroupMember middleware: no group in context")
	}
	m, ok := r.Context().Value(groupMemberKey).(store.GroupMember)
	if !ok {
		panic("handler requires requireGroupMember middleware: no group member in context")
	}
	return authedMember{group: g, member: m}
}
