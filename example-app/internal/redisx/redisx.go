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

// SetNX melakukan SET key value NX EX ttlSec: hanya berhasil bila key belum ada.
// Dipakai sebagai kunci (lock) terdistribusi oleh scheduler-service.
func (c *Client) SetNX(key, value string, ttlSec int) (bool, error) {
	v, err := c.do("SET", key, value, "NX", "EX", strconv.Itoa(ttlSec))
	if err != nil {
		return false, err
	}
	s, ok := v.(string)
	return ok && s == "OK", nil
}

func (c *Client) Incr(key string) (int64, error) {
	v, err := c.do("INCR", key)
	if err != nil {
		return 0, err
	}
	n, _ := v.(int64)
	return n, nil
}

// TTL mengembalikan sisa umur key dalam detik (-1 bila tanpa expire, -2 bila tidak ada).
func (c *Client) TTL(key string) (int64, error) {
	v, err := c.do("TTL", key)
	if err != nil {
		return 0, err
	}
	n, _ := v.(int64)
	return n, nil
}

// IncrWindow adalah penghitung FIXED WINDOW: INCR key; bila ini hit pertama
// di jendela (nilai==1) pasang EXPIRE windowSec. Mengembalikan hitungan saat
// ini dan sisa detik jendela (untuk header Retry-After rate limiter).
// Catatan belajar: rangkaian INCR→EXPIRE→TTL ini tidak atomik 100% (beda
// dengan skrip Lua), tetapi cukup & transparan untuk rate limit contoh ini.
func (c *Client) IncrWindow(key string, windowSec int) (int64, int64, error) {
	n, err := c.Incr(key)
	if err != nil {
		return 0, 0, err
	}
	if n == 1 {
		if _, err := c.do("EXPIRE", key, strconv.Itoa(windowSec)); err != nil {
			return n, int64(windowSec), nil
		}
		return n, int64(windowSec), nil
	}
	ttl, err := c.TTL(key)
	if err != nil || ttl < 0 {
		return n, int64(windowSec), nil
	}
	return n, ttl, nil
}
