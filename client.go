package pusherws

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const ProtocolVersion = 7

type Client struct {
	AppKey  string
	conn    *websocket.Conn
	baseURL string

	SocketID string

	// read side
	Events     chan *Event
	ReadErrors chan error

	// write side
	writeEvents chan *writeRequest
	writeErrors chan error

	bufPool      *sync.Pool
	eventParsers map[string]EventDataParser

	pingInterval time.Duration

	bindings     map[binding][]chan *Event
	bindingsLock *sync.RWMutex

	authCB AuthCallback
}

type AuthCallback func(socketID, channel string) (string, error)

type EventDataParser func(data json.RawMessage) (any, error)

func NewClient(appKey, baseURL string, authCB AuthCallback) (*Client, error) {
	return &Client{
		AppKey:      appKey,
		baseURL:     baseURL,
		Events:      make(chan *Event, 100),
		ReadErrors:  make(chan error, 1),
		writeEvents: make(chan *writeRequest, 100),
		writeErrors: make(chan error, 1),
		bufPool: &sync.Pool{
			New: func() any {
				return new(bytes.Buffer)
			},
		},
		eventParsers: defaultParsers(),
		bindings:     make(map[binding][]chan *Event),
		bindingsLock: &sync.RWMutex{},
		authCB:       authCB,
	}, nil
}

func (c *Client) connectURL() (string, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid baseURL: %w", err)
	}
	parsed = parsed.JoinPath("app", c.AppKey)
	query := url.Values{}
	query.Add("protocol", strconv.Itoa(ProtocolVersion))
	// TODO client and version
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (c *Client) Connect(ctx context.Context) error {
	dialer := &websocket.Dialer{}
	connectTo, err := c.connectURL()
	if err != nil {
		return err
	}
	conn, _, err := dialer.DialContext(ctx, connectTo, nil)
	if err != nil {
		return err
	}
	c.conn = conn
	c.conn.SetPingHandler(c.pingHandler)

	// wait for EConnectionEstablished returning
	_, r, err := c.conn.NextReader()
	if err != nil {
		return fmt.Errorf("failed waiting for connection established: %w", err)
	}
	event, err := c.readEvent(r)
	if err != nil {
		return fmt.Errorf("failed parsing event: %w", err)
	}
	if event.Event != EConnectionEstablished {
		return fmt.Errorf("first event isn't %v: is %v", EConnectionEstablished, event.Event)
	}
	cm, ok := event.Data.(*ConnectionMetadata)
	if ok {
		c.SocketID = cm.SocketID
		c.pingInterval = time.Duration(cm.ActivityTimeout-1) * time.Second
	}

	// start the normal reader and writer goroutines
	go c.eventReader()
	go c.eventWriter()

	return nil
}

func (c *Client) pingHandler(message string) error {
	log.Printf("Got ping <%v>", message)
	err := c.conn.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(time.Second))
	if err == websocket.ErrCloseSent {
		return nil
	} else if _, ok := err.(net.Error); ok {
		return nil
	}
	log.Printf("Sent pong")
	return err
}

func (c *Client) eventReader() {
	var err error
	var r io.Reader
	for {
		_, r, err = c.conn.NextReader()
		if err != nil {
			break
		}
		event, err := c.readEvent(r)
		if err != nil {
			log.Printf("failed reading event: %v", err)
			c.sendReadError(err)
			continue
		}
		c.processEvents(event)
		select {
		case c.Events <- event:
		default:
			log.Printf("event recv buffer full! dropping ")
		}
	}
	log.Printf("exiting read: %v", err)
	// TODO cleanup
	select {
	case c.ReadErrors <- err:
	default:
		log.Printf("unhandled terminal read error: %v", err)
	}
	close(c.Events)
	close(c.ReadErrors)
	close(c.writeEvents)
	//log.Fatalf("read error: %T %v", err, err)
}

func (c *Client) readEvent(r io.Reader) (*Event, error) {
	buf := c.poolGet()
	defer c.poolPut(buf)
	_, err := io.Copy(buf, r)
	if err != nil {
		return nil, err
	}
	log.Printf("rcv raw: %v", buf.String())

	revent := &rawEvent{}
	err = json.Unmarshal(buf.Bytes(), revent)
	if err != nil {
		return nil, err
	}
	return c.parseKnown(revent)
}

func (c *Client) poolGet() *bytes.Buffer {
	return c.bufPool.Get().(*bytes.Buffer)
}

func (c *Client) poolPut(buf *bytes.Buffer) {
	buf.Reset()
	c.bufPool.Put(buf)
}

func (c *Client) processEvents(evt *Event) {
	switch evt.Event {
	case EPing:
		c.sendPong()
	}
	// do the bindings
	b := binding{
		Event:   evt.Event,
		Channel: evt.Channel,
	}
	c.bindingsLock.RLock()
	defer c.bindingsLock.RUnlock()
	c.triggerBindings(c.bindings[b], evt)
	if evt.Channel != "" {
		b.Channel = ""
		c.triggerBindings(c.bindings[b], evt)
	}
}

func (c *Client) triggerBindings(evtChans []chan *Event, evt *Event) {
	for _, evtChan := range evtChans {
		select {
		case evtChan <- evt:
		default:
		}
	}
}

func (c *Client) sendReadError(err error) {
	select {
	case c.ReadErrors <- err:
	default:
		log.Printf("unhandled read error: %v", err)
	}
}

func (c *Client) parseKnown(rawEvent *rawEvent) (*Event, error) {
	var anyVal any
	var err error

	parser, ok := c.eventParsers[rawEvent.Event]
	if ok {
		anyVal, err = parser(rawEvent.Data)
		if err != nil {
			return nil, err
		}
	} else {
		anyVal = string(rawEvent.Data)
	}

	return &Event{
		Event:   rawEvent.Event,
		Channel: rawEvent.Channel,
		Data:    anyVal,
	}, nil
}

type rawEvent struct {
	Event   string          `json:"event"`
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) sendWriteError(errChan chan error, err error) {
	if errChan != nil {
		select {
		case errChan <- err:
		default:
			log.Printf("unhandled write error %T: %v", err, err)
		}
	}
}

func (c *Client) eventWriter() {
	//for evt := range c.writeEvents {
	pinger := time.NewTimer(c.pingInterval)
	closed := false
	for !closed {
		select {
		case wr := <-c.writeEvents:
			if wr == nil {
				closed = true
				break
			}
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				c.sendWriteError(wr.ErrChan, err)
				closed = true
				break
			}

			err = c.handleWrite(w, wr.Event)
			if err != nil {
				c.sendWriteError(wr.ErrChan, err)
				continue
			}
			// unblock anything waiting for errors
			c.sendWriteError(wr.ErrChan, nil)
			pinger.Reset(c.pingInterval)
		case <-pinger.C:
			c.sendPing()
			pinger.Reset(c.pingInterval)
		}
	}
}

func (c *Client) sendPing() {
	c.SendEvent(&Event{
		Event: EPing,
	}, nil)
	/* Native pings don't seem to keep the connection open to soketi
	err := c.conn.WriteControl(websocket.PingMessage, []byte("yo wake up"), time.Now().Add(time.Second))
	if err != nil {
		log.Printf("ping error: %v", err)
	}
	*/
}

func (c *Client) sendPong() {
	c.SendEvent(&Event{
		Event: EPong,
	}, nil)
}

func (c *Client) handleWrite(w io.WriteCloser, evt *Event) error {
	defer w.Close()
	data, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	log.Printf("sending: %v", string(data))
	_, err = w.Write(data)
	return err
}

func (c *Client) Subscribe(channel string) error {
	subdata := SubscribeData{
		Channel: channel,
	}
	if strings.HasPrefix(channel, "private-") && c.authCB != nil {
		log.Println("adding authdata on sub")
		auth, err := c.authCB(channel, c.SocketID)
		if err != nil {
			return fmt.Errorf("failed auth: %w", err)
		}
		subdata.Auth = auth
	}

	// bind to the response before sending the subscription
	respChan := make(chan *Event, 10)
	c.Bind(ESubscriptionSucceeded, "", respChan)
	defer c.Unbind(ESubscriptionSucceeded, "", respChan)
	c.Bind(ESubscriptionError, "", respChan)
	defer c.Unbind(ESubscriptionError, "", respChan)

	// Send Subscribe Event
	evt := &Event{
		Event: ESubscribe,
		Data:  subdata,
	}
	err := c.SendEventBlocking(evt)
	if err != nil {
		return err
	}

	// block for confirmation
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case subStatus := <-respChan:
			if subStatus.Channel == channel {
				if subStatus.Event == ESubscriptionSucceeded {
					return nil
				}
				if subStatus.Event == ESubscriptionError {
					errData, ok := subStatus.Data.(*SubscriptionErrorData)
					if ok {
						return fmt.Errorf("channel %v subscription failed: %v", channel, errData)
					} else {
						return fmt.Errorf("unknown subscription error for %v", channel)
					}
				}
			}
		case <-timeout:
			return fmt.Errorf("subscription to %v timed out", channel)
		}
	}
}

func (c *Client) UnSubscribe(channel string) error {
	c.UnbindChannel(channel)
	evt := &Event{
		Event: EUnsubscribe,
		Data: SubscribeData{
			Channel: channel,
		},
	}
	return c.SendEventBlocking(evt)
	// TODO maybe block for confirmation? it appears Soketi doesn't send anything back on unsubscribe
}

// UnbindChannel removes all bindings for a channel
func (c *Client) UnbindChannel(channel string) {
	c.bindingsLock.Lock()
	defer c.bindingsLock.Unlock()
	for key := range c.bindings {
		if key.Channel == channel {
			delete(c.bindings, key)
		}
	}
}

type writeRequest struct {
	Event   *Event
	ErrChan chan error
}

func (c *Client) SendEvent(event *Event, errChan chan error) {
	c.writeEvents <- &writeRequest{
		Event:   event,
		ErrChan: errChan,
	}
}

func (c *Client) SendEventBlocking(event *Event) error {
	errChan := make(chan error, 1)
	c.SendEvent(event, errChan)
	return <-errChan
}

// type waitFor struct {
// 	MatchEvent *Event
// 	Timeout    time.Duration
// 	RespChan   chan *waitForResponse
// }

// type waitForResponse struct {
// 	Event *Event
// 	Error error
// }

// func WaitFor(match *Event) (*Event, error) {

// }

type binding struct {
	Event   string
	Channel string
}

func (c *Client) BindGlobal(eventName string, eventChan chan *Event) {
	c.Bind(eventName, "", eventChan)
}

func (c *Client) UnbindGlobal(eventName string, eventChan chan *Event) {
	c.Unbind(eventName, "", eventChan)
}

// Bind will cause events matching eventName and channel to be sent to eventChan
// an empty channel will match all eventName on any channel
func (c *Client) Bind(eventName, channel string, eventChan chan *Event) {
	c.bindingsLock.Lock()
	defer c.bindingsLock.Unlock()
	b := binding{
		Event:   eventName,
		Channel: channel,
	}
	c.bindings[b] = append(c.bindings[b], eventChan)
}

// Unbind removes a binding matching the eventName, channel and eventChan.
// if eventChan is nil, all binding are remove matching eventName and channel.
func (c *Client) Unbind(eventName, channel string, eventChan chan *Event) {
	c.bindingsLock.Lock()
	defer c.bindingsLock.Unlock()
	b := binding{
		Event:   eventName,
		Channel: channel,
	}
	eventChans := c.bindings[b]
	if eventChan == nil || len(eventChans) < 2 {
		delete(c.bindings, b)
	} else {
		newEventChans := make([]chan *Event, 0, len(eventChans))
		for _, ec := range eventChans {
			if ec != eventChan {
				newEventChans = append(newEventChans, ec)
			}
		}
		c.bindings[b] = newEventChans
	}
}
