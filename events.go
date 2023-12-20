package pusherws

import (
	"encoding/json"
	"fmt"
)

// {"event":"pusher_internal:subscription_succeeded","channel":"private-fake-channel"}
// {"event":"pusher:subscription_error","channel":"private-location-5404dd8c-c63b-471e-bf3f-6dcff955f3de","data":{"type":"AuthError","error":"The connection is unauthorized.","status":401}}
const (
	EConnectionEstablished = "pusher:connection_established"
	EError                 = "pusher:error"
	EPing                  = "pusher:ping"
	EPong                  = "pusher:pong"
	ESubscribe             = "pusher:subscribe"
	ESubscriptionSucceeded = "pusher_internal:subscription_succeeded"
	ESubscriptionError     = "pusher:subscription_error"
	EUnsubscribe           = "pusher:unsubscribe"
	EUnsubscribed          = "pusher_internal:unsubscribed" // Socketi doesn't seem to send this
	ESignin                = "pusher:signin"
	ESigninSuccess         = "pusher:signin_success"
	EMemberAdded           = "pusher:member_added"
	EMemberRemoved         = "pusher:member_removed"
	EIMemberAdded          = "pusher_internal:member_added"
	EIMemberRemoved        = "pusher_internal:member_removed"

	ECacheMiss = "pusher:cache_miss" // found in Soketi code; no idea if this is relevant
)

type Event struct {
	Event   string `json:"event"`
	Channel string `json:"channel,omitempty"`
	Data    any    `json:"data,omitempty"`
}

func defaultParsers() map[string]EventDataParser {
	return map[string]EventDataParser{
		EConnectionEstablished: parserConnectionMetadata,
		EError:                 parseErrorData,
		ESubscriptionError:     parseSubscriptionErrorData,
	}
}

// ConnectionMetadata data filed for pusher:connection_established
type ConnectionMetadata struct {
	SocketID        string `json:"socket_id"`
	ActivityTimeout int    `json:"activity_timeout"`
}

func parserConnectionMetadata(data json.RawMessage) (any, error) {
	cm := &ConnectionMetadata{}
	err := DoubleDecodeJSON(data, cm)
	if err != nil {
		return nil, err
	}
	return cm, nil
}

// ErrorData data filed for ErrorData
type ErrorData struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func parseErrorData(data json.RawMessage) (any, error) {
	errData := &ErrorData{}
	err := json.Unmarshal(data, errData)
	if err != nil {
		return nil, err
	}
	return errData, nil
}

// DoubleDecodeJSON some of the values returned by the pusher protocol
// have double encoded JSON... meh
func DoubleDecodeJSON(data []byte, val any) error {
	var stringVal string
	err := json.Unmarshal(data, &stringVal)
	if err != nil {
		return fmt.Errorf("not double decoded: %w", err)
	}
	err = json.Unmarshal([]byte(stringVal), val)
	if err != nil {
		return fmt.Errorf("failed decode into %T: %w", val, err)
	}
	return nil
}

type SubscribeData struct {
	Channel     string `json:"channel"`
	Auth        string `json:"auth,omitempty"`
	ChannelData string `json:"channel_data,omitempty"`
}

type SubscriptionErrorData struct {
	Type   string `json:"type"`
	Error  string `json:"error"`
	Status int    `json:"status"`
}

func (sed *SubscriptionErrorData) String() string {
	return fmt.Sprintf("%v %v", sed.Type, sed.Error)
}

func parseSubscriptionErrorData(data json.RawMessage) (any, error) {
	errData := &SubscriptionErrorData{}
	err := json.Unmarshal(data, errData)
	if err != nil {
		return nil, err
	}
	return errData, nil
}
