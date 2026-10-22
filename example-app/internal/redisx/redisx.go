// Package redisx adalah client Redis minimal (RESP) buatan sendiri dari stdlib.
// Cukup untuk belajar: GET, SETEX, DEL, INCR, PING.
package redisx

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type Client struct{ Addr string }

func (c *Client) do(args ...string) (any, error) {
	conn, err := net.DialTimeout("tcp", c.Addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := conn.Write([]byte(b.String())); err != nil {
		return nil, err
	}
	return readResp(bufio.NewReader(conn))
}

func readResp(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	switch prefix {
	case '+':
		return line, nil
	case '-':
		return nil, errors.New("redis: " + line)
	case ':':
		n, _ := strconv.ParseInt(line, 10, 64)
		return n, nil
	case '$':
		n, _ := strconv.Atoi(line)
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		total := 0
		for total < len(buf) {
			m, err := r.Read(buf[total:])
			if err != nil {
				return nil, err
			}
			total += m
		}
		return string(buf[:n]), nil
	case '*':
		n, _ := strconv.Atoi(line)
		if n < 0 {
			return nil, nil
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			v, err := readResp(r)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	return nil, fmt.Errorf("redis: prefix tidak dikenal %q", prefix)
}

func (c *Client) Ping() error {
	_, err := c.do("PING")
	return err
}

// Get mengembalikan (nilai, ada, error).
func (c *Client) Get(key string) (string, bool, error) {
	v, err := c.do("GET", key)
	if err != nil || v == nil {
		return "", false, err
	}
	s, _ := v.(string)
	return s, true, nil
}

func (c *Client) SetEX(key string, seconds int, value string) error {
	_, err := c.do("SETEX", key, strconv.Itoa(seconds), value)
	return err
}

func (c *Client) Del(key string) error {
	_, err := c.do("DEL", key)
	return err
}

func (c *Client) Incr(key string) (int64, error) {
	v, err := c.do("INCR", key)
	if err != nil {
		return 0, err
	}
	n, _ := v.(int64)
	return n, nil
}
