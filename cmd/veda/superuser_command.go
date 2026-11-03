package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/term"
)

func createSuperuserCommand(args []string) error {
	flags := flag.NewFlagSet("createsuperuser", flag.ContinueOnError)
	username := flags.String("username", "", "Administrator username")
	socket := flags.String("socket", "/run/veda/reader.sock", "VEDA local Unix socket")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("createsuperuser requires an interactive terminal")
	}
	if *username == "" {
		fmt.Fprint(os.Stderr, "Username: ")
		if _, err := fmt.Fscanln(os.Stdin, username); err != nil {
			return errors.New("username input failed")
		}
	}
	if !identifier.MatchString(*username) {
		return errors.New("invalid username")
	}
	fmt.Fprint(os.Stderr, "Password: ")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return errors.New("password input failed")
	}
	fmt.Fprint(os.Stderr, "Password (again): ")
	confirm, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return errors.New("password confirmation failed")
	}
	if !bytes.Equal(password, confirm) {
		return errors.New("passwords do not match")
	}
	if !passwordValid(string(password)) {
		return errors.New("password must contain 10–128 characters")
	}
	if err := submitSuperuser(*socket, *username, string(password)); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Administrator account created.")
	return nil
}

func submitSuperuser(socket, username, password string) error {
	body, err := json.Marshal(struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}{username, password})
	if err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Post("http://localhost/api/local/createsuperuser", "application/json", bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot reach the local VEDA socket")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusConflict:
		return errors.New("user already exists or cannot be activated")
	case http.StatusBadRequest:
		return errors.New("invalid username or password")
	default:
		return errors.New("VEDA rejected the request; run as root on the VEDA host")
	}
}
