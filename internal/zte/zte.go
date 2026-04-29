// Package zte talks to the web API of a ZTE CPE router (ZX297520V3).
//
// Endpoints:
//
//	POST /goform/goform_set_cmd_process  -> actions (LOGIN, SEND_SMS, ...)
//	GET  /goform/goform_get_cmd_process  -> queries (cmd=...)
//
// Login: username and password are base64-encoded (PASSWORD_ENCODE=true on
// this firmware). Session: standard HTTP cookie kept by http.Client + cookiejar.
package zte

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// lockoutThreshold caps consecutive auth failures before login() refuses to
// hit the wire. The router locks the account after 5 failed logins; cutting
// at 3 leaves margin for a manual restart.
const lockoutThreshold = 3

type Client struct {
	base string
	http *http.Client
	user string
	pass string

	mu                   sync.Mutex
	loggedIn             bool
	consecutiveAuthFails int
}

// Message is a received SMS, already decoded.
type Message struct {
	ID     int            `json:"id"`
	Number string         `json:"number"`
	Body   string         `json:"body"`
	TS     string         `json:"ts"`
	Tag    int            `json:"tag"`
	Raw    map[string]any `json:"raw"`
}

func New(host, user, pass string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		base: "http://" + host,
		user: user,
		pass: pass,
		http: &http.Client{Jar: jar, Timeout: 10 * time.Second},
	}, nil
}

func (c *Client) post(form url.Values) (map[string]any, error) {
	return c.do(http.MethodPost, "/goform/goform_set_cmd_process", form)
}

func (c *Client) get(params url.Values) (map[string]any, error) {
	return c.do(http.MethodGet, "/goform/goform_get_cmd_process", params)
}

func (c *Client) do(method, path string, values url.Values) (map[string]any, error) {
	var (
		req *http.Request
		err error
	)
	switch method {
	case http.MethodPost:
		req, err = http.NewRequest(method, c.base+path, strings.NewReader(values.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	case http.MethodGet:
		req, err = http.NewRequest(method, c.base+path+"?"+values.Encode(), nil)
	default:
		return nil, fmt.Errorf("unsupported method: %s", method)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", c.base+"/index.html")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("invalid JSON from router: %w (body=%s)", err, body)
	}
	return out, nil
}

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func (c *Client) login() error {
	if c.consecutiveAuthFails >= lockoutThreshold {
		return fmt.Errorf("login disabled after %d consecutive auth failures (manual intervention required to avoid router lockout at 5)", c.consecutiveAuthFails)
	}
	res, err := c.post(url.Values{
		"isTest":   {"false"},
		"goformId": {"LOGIN"},
		"username": {b64(c.user)},
		"password": {b64(c.pass)},
	})
	if err != nil {
		// Transport error — the server didn't refuse our credentials, so
		// don't bump the counter.
		return err
	}
	r, _ := res["result"].(string)
	// "0" = ok, "4" = already logged in.
	if r != "0" && r != "4" {
		// Bump on any non-OK result. A tighter alternative would gate on
		// result == "3" (bad credentials), but the router's response codes
		// aren't documented exhaustively. Cost of a false positive is
		// "operator restarts the daemon"; cost of a false negative is the
		// router locking us out at 5 failures.
		c.consecutiveAuthFails++
		return fmt.Errorf("login failed (auth fail %d/%d): %v", c.consecutiveAuthFails, lockoutThreshold, res)
	}
	c.consecutiveAuthFails = 0
	c.loggedIn = true
	return nil
}

func (c *Client) ensureSession() error {
	if c.loggedIn {
		return nil
	}
	return c.login()
}

// withSession ensures a session, runs fn; if fn fails, assumes the session is
// stale, re-logs in and retries ONCE.
func (c *Client) withSession(fn func() error) error {
	if err := c.ensureSession(); err != nil {
		return err
	}
	err := fn()
	if err == nil {
		return nil
	}
	c.loggedIn = false
	if err2 := c.ensureSession(); err2 != nil {
		return err2
	}
	return fn()
}

// encodeMessage mirrors encodeMessage + getEncodeType from the firmware.
// Always UNICODE (UCS-2 BE) — the modem accepts it and we avoid porting the
// full GSM7_Table.
func encodeMessage(text string) string {
	units := utf16.Encode([]rune(text))
	buf := make([]byte, 2*len(units))
	for i, u := range units {
		binary.BigEndian.PutUint16(buf[i*2:], u)
	}
	return strings.ToUpper(hex.EncodeToString(buf))
}

// nowString builds the firmware's getCurrentTimeString format:
// "YY;MM;DD;HH;MM;SS;+TZ"  (TZ in hours, signed).
func nowString() string {
	t := time.Now()
	_, offsetSec := t.Zone()
	tz := offsetSec / 3600
	sign := "+"
	if tz < 0 {
		sign = ""
	}
	return fmt.Sprintf("%02d;%02d;%02d;%02d;%02d;%02d;%s%d",
		t.Year()%100, int(t.Month()), t.Day(),
		t.Hour(), t.Minute(), t.Second(), sign, tz)
}

func (c *Client) SendSMS(number, message string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.withSession(func() error {
		res, err := c.post(url.Values{
			"isTest":      {"false"},
			"goformId":    {"SEND_SMS"},
			"notCallback": {"true"},
			"Number":      {number},
			"sms_time":    {nowString()},
			"MessageBody": {encodeMessage(message)},
			"ID":          {"-1"},
			"encode_type": {"UNICODE"},
		})
		if err != nil {
			return err
		}
		if r, _ := res["result"].(string); r != "success" {
			return fmt.Errorf("router rejected SEND_SMS: %v", res)
		}
		return nil
	})
}

func (c *Client) Status() (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out map[string]any
	err := c.withSession(func() error {
		r, err := c.get(url.Values{
			"isTest":     {"false"},
			"multi_data": {"1"},
			"cmd":        {"loginfo,sms_received_flag,sms_unread_num,signalbar,network_type"},
		})
		if err != nil {
			return err
		}
		out = r
		return nil
	})
	return out, err
}

// listSMS — raw endpoint. memStore: 1=NV (router memory), 0=SIM.
// tags: 10=all messages from that store.
func (c *Client) listSMS(memStore int) (map[string]any, error) {
	return c.get(url.Values{
		"isTest":        {"false"},
		"cmd":           {"sms_data_total"},
		"page":          {"0"},
		"data_per_page": {"500"},
		"mem_store":     {strconv.Itoa(memStore)},
		"tags":          {"10"},
		"order_by":      {"order by id desc"},
	})
}

// Inbox returns received SMS (tag 0=unread or 1=read), decoded.
// Filters out incomplete concat SMS (received_all_concat_sms == "0").
func (c *Client) Inbox(memStore int) ([]Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Message
	err := c.withSession(func() error {
		res, err := c.listSMS(memStore)
		if err != nil {
			return err
		}
		raw, _ := res["messages"].([]any)
		out = out[:0]
		for _, m := range raw {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			if s, _ := mm["received_all_concat_sms"].(string); s == "0" {
				continue
			}
			tag, _ := strconv.Atoi(asString(mm["tag"]))
			if tag != 0 && tag != 1 {
				continue
			}
			id, _ := strconv.Atoi(asString(mm["id"]))
			out = append(out, Message{
				ID:     id,
				Number: asString(mm["number"]),
				Body:   DecodeMessage(asString(mm["content"])),
				TS:     ParseDate(asString(mm["date"])),
				Tag:    tag,
				Raw:    mm,
			})
		}
		return nil
	})
	return out, err
}

func (c *Client) Delete(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.withSession(func() error {
		var b strings.Builder
		for _, id := range ids {
			fmt.Fprintf(&b, "%d;", id)
		}
		res, err := c.post(url.Values{
			"isTest":      {"false"},
			"goformId":    {"DELETE_SMS"},
			"msg_id":      {b.String()},
			"notCallback": {"true"},
		})
		if err != nil {
			return err
		}
		if r, _ := res["result"].(string); r != "success" {
			return fmt.Errorf("router rejected DELETE_SMS: %v", res)
		}
		return nil
	})
}

func (c *Client) Logout() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.post(url.Values{
		"isTest":   {"false"},
		"goformId": {"LOGOUT"},
	})
	c.loggedIn = false
	return err
}

// DecodeMessage is the inverse of encodeMessage: UCS-2 BE hex -> string.
func DecodeMessage(hexStr string) string {
	if hexStr == "" {
		return ""
	}
	if len(hexStr)%2 == 1 {
		hexStr = "0" + hexStr
	}
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return hexStr
	}
	if len(b)%2 == 1 {
		b = append([]byte{0}, b...)
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(units))
}

// ParseDate converts the router's format ('YY,MM,DD,HH,MM,SS,±TZ', with TZ in
// quarter-hours, not hours) into ISO 8601 with offset. If parsing fails, the
// original string is returned.
func ParseDate(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(strings.ReplaceAll(s, ";", ","), ",")
	if len(parts) < 6 {
		return s
	}
	nums := make([]int, 6)
	for i := 0; i < 6; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return s
		}
		nums[i] = n
	}
	year := 2000 + nums[0]
	if nums[0] >= 70 {
		year = 1900 + nums[0]
	}
	tzQuarters := 0
	if len(parts) > 6 && parts[6] != "" {
		if q, err := strconv.Atoi(parts[6]); err == nil {
			tzQuarters = q
		}
	}
	loc := time.FixedZone("router", tzQuarters*15*60)
	t := time.Date(year, time.Month(nums[1]), nums[2],
		nums[3], nums[4], nums[5], 0, loc)
	return t.Format(time.RFC3339)
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}
