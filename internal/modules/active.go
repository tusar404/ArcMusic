/*
 * ○ A high-performance engine for streaming music in Telegram voicechats.
 *
 * Copyright (C) 2026 Team Arc
 */

package modules

import (
	"github.com/amarnathcjd/gogram/telegram"

	"main/internal/core"
	"main/internal/locales"
)

func init() {
	helpTexts["/active"] = `Show all active voice chat sessions.

<u>Usage:</u>
<b>/active</b> or <b>/ac</b> — List active chats

<b>📊 Information Shown:</b>
• Total active voice chats

<b>🔒 Restrictions:</b>
• <b>Sudo users</b> only

<b>💡 Use Case:</b>
Monitor exact bot usage.`

	keys := []string{"/ac", "/activevc", "/activevoice"}
	for _, k := range keys {
		helpTexts[k] = helpTexts["/active"]
	}
}

func activeHandler(m *telegram.NewMessage) error {
	chatID := m.ChannelID()

	activeCount := 0

	// Iterate through all tracked room states natively in Go
	for _, room := range core.GetAllRooms() {
		if room != nil && room.IsActiveChat() {
			activeCount++
		}
	}

	// Send the exact count
	msg := F(chatID, "active_chats_info", locales.Arg{
		"count": activeCount,
	})

	m.Reply(msg)
	return telegram.ErrEndGroup
}
