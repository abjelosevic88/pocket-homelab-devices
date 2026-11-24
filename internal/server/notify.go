package server

import (
	"context"
	"fmt"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/notify"
)

// notifyUser sends one person a message through their own notification
// address, when they want this kind ("offline", "refused"; "" = always).
func (s *Server) notifyUser(ctx context.Context, uid int64, kind, title, body, tag string, deviceID int64) {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil || st.NotifyURL == "" {
		return
	}
	switch kind {
	case "offline":
		if !st.NotifyOffline {
			return
		}
	case "refused":
		if !st.NotifyRefused {
			return
		}
	}
	prio := "info"
	if kind != "" && tag != "white_check_mark" {
		prio = "warning"
	}
	link := ""
	if s.cfg.BaseURL != "" && deviceID > 0 {
		link = fmt.Sprintf("%s/devices/%d", s.cfg.BaseURL, deviceID)
	}
	if err := notify.New(st.NotifyURL, st.NotifyFormat).Send(ctx, notify.Message{Title: title, Body: body, Priority: prio, Tags: []string{tag}, Link: link}); err != nil {
		s.log.Warn("notify failed", "user", uid, "err", err)
	}
}
