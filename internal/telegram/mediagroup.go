package telegram

import (
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// MediaGroupCollector collects messages sharing the same media_group_id and flushes them as a batch after Delay.
// This is needed to treat Telegram albums as one "unit of work".
type MediaGroupCollector struct {
	Delay time.Duration
	OnFlush func(groupID string, msgs []*tgbotapi.Message)

	mu     sync.Mutex
	groups map[string]*mediaGroup
}

type mediaGroup struct {
	timer *time.Timer
	msgs  []*tgbotapi.Message
}

func NewMediaGroupCollector(delay time.Duration, onFlush func(groupID string, msgs []*tgbotapi.Message)) *MediaGroupCollector {
	if delay <= 0 {
		delay = 2 * time.Second
	}
	return &MediaGroupCollector{
		Delay:  delay,
		OnFlush: onFlush,
		groups: make(map[string]*mediaGroup),
	}
}

func (c *MediaGroupCollector) Collect(msg *tgbotapi.Message) {
	if c == nil || msg == nil || msg.MediaGroupID == "" {
		return
	}
	groupID := msg.MediaGroupID

	c.mu.Lock()
	defer c.mu.Unlock()

	g, ok := c.groups[groupID]
	if !ok {
		g = &mediaGroup{}
		c.groups[groupID] = g
		g.timer = time.AfterFunc(c.Delay, func() {
			c.flush(groupID)
		})
	}
	g.msgs = append(g.msgs, msg)

	// Extend the timer on each new message to catch late arrivals.
	if g.timer != nil {
		g.timer.Reset(c.Delay)
	}
}

func (c *MediaGroupCollector) flush(groupID string) {
	var batch []*tgbotapi.Message

	c.mu.Lock()
	g, ok := c.groups[groupID]
	if ok {
		delete(c.groups, groupID)
		if g.timer != nil {
			g.timer.Stop()
		}
		batch = g.msgs
	}
	c.mu.Unlock()

	if ok && c.OnFlush != nil && len(batch) > 0 {
		c.OnFlush(groupID, batch)
	}
}

