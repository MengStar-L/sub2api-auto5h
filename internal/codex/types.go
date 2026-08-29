package codex

import (
	"fmt"
	"time"
)

const (
	FiveHourMinutes = 300
	SevenDayMinutes = 10_080
	MaxReplyRunes   = 2_000
	MaxSSEBytes     = 1 << 20
)

const WakeupPrompt = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

苹果味 桃子味 西瓜味
圆形 7 9 8
五角星形 7 6 4`

const NumericOnlyInstruction = "只能返回一个阿拉伯数字，不要解释，不要添加标点或其他内容。"

type Proxy struct {
	Protocol string
	Host     string
	Port     int
	Username string
	Password string
}

type Request struct {
	AccessToken string
	AccountID   string
	UserAgent   string
	Model       string
	Proxy       *Proxy
	Timeout     time.Duration
}

type RateWindow struct {
	UsedPercent       float64
	ResetAfterSeconds int64
	ResetAt           int64
}

type RateLimits struct {
	FiveHour *RateWindow
	SevenDay *RateWindow
}

type Result struct {
	HTTPStatus    int
	Reply         string
	Terminal      string
	TransportPath string
	RateLimits    RateLimits
}

type ErrorKind string

const (
	ErrorUnauthorized ErrorKind = "unauthorized"
	ErrorForbidden    ErrorKind = "forbidden"
	ErrorNotFound     ErrorKind = "not_found"
	ErrorRateLimited  ErrorKind = "rate_limited"
	ErrorTransient    ErrorKind = "transient"
	ErrorSchema       ErrorKind = "schema"
	ErrorRejected     ErrorKind = "rejected"
)

type Error struct {
	Kind       ErrorKind
	StatusCode int
	Code       string
	Message    string
	RateLimits RateLimits
	Cause      error
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("codex %s (%s): %s", e.Kind, e.Code, e.Message)
	}
	return fmt.Sprintf("codex %s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func IsTransient(err error) bool {
	value, ok := err.(*Error)
	return ok && value.Kind == ErrorTransient
}
