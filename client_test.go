package pusherws

import (
	"log"
	"testing"
)

func TestBaseURL(t *testing.T) {
	c, err := NewClient("123456", "ws://ws.example.com", nil)
	if err != nil {
		t.Fatalf("NewClient err: %v", err)
	}
	curl, err := c.connectURL()
	if err != nil {
		t.Fatalf("NewClient err: %v", err)
	}
	if curl != "ws://ws.example.com/app/123456?protocol=7" {
		log.Fatalf("wrong url value: %v", curl)
	}
}
