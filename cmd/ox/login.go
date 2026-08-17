package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/term"

	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
)

func login(
	ctx context.Context,
	input *os.File,
	output io.Writer,
	store *credentials.Store,
	client *openrouter.Client,
) error {
	if store.KeyringDisabled() {
		return errors.New(credentials.KeyringDisabledMessage)
	}

	key, err := readCredential(input, output)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("OpenRouter API key is empty")
	}
	if err := client.VerifyCredential(ctx, key); err != nil {
		return fmt.Errorf("verify OpenRouter API key: %w", err)
	}
	if err := store.Set(key); err != nil {
		return err
	}

	if store.Source() == credentials.SourceEnvironment {
		fmt.Fprintln(output, "Stored the OpenRouter API key in the OS keyring; OPENROUTER_API_KEY still supplies the credential.")
		return nil
	}
	fmt.Fprintln(output, "Stored the OpenRouter API key in the OS keyring.")
	return nil
}

func readCredential(input *os.File, output io.Writer) (string, error) {
	fd := int(input.Fd())
	if !term.IsTerminal(fd) {
		key, err := bufio.NewReader(input).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read OpenRouter API key: %w", err)
		}
		return key, nil
	}

	// ReadPassword turns echo off and back on with a deferred call, which the
	// default handling of an interrupt skips, leaving the terminal unable to
	// echo what the user types next.
	state, err := term.GetState(fd)
	if err != nil {
		return "", fmt.Errorf("read terminal state: %w", err)
	}
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	defer signal.Stop(interrupted)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-interrupted:
			_ = term.Restore(fd, state)
			os.Exit(1)
		case <-done:
		}
	}()

	if _, err := fmt.Fprint(output, "OpenRouter API key: "); err != nil {
		return "", fmt.Errorf("write login prompt: %w", err)
	}
	key, err := term.ReadPassword(fd)
	if _, newlineErr := fmt.Fprintln(output); err == nil {
		err = newlineErr
	}
	if err != nil {
		return "", fmt.Errorf("read OpenRouter API key: %w", err)
	}
	return string(key), nil
}
