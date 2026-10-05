package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// A missing provider uses the destination Codex provider. Blank the JSON member
// with spaces so every fork reference and history projection byte offset stays
// valid, even when the destination provider name is longer than the source name.
func adaptSessionProvider(filePath, provider string) (bool, error) {
	input, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	line, err := bufio.NewReader(input).ReadBytes('\n')
	_ = input.Close()
	if err != nil && err != io.EOF {
		return false, err
	}
	adapted, err := providerNeutralHeader(line, provider)
	if err != nil || bytes.Equal(line, adapted) {
		return false, err
	}
	output, err := os.OpenFile(filePath, os.O_WRONLY, 0)
	if err != nil {
		return false, err
	}
	defer output.Close()
	if _, err := output.WriteAt(adapted, 0); err != nil {
		return false, err
	}
	return true, output.Sync()
}

func providerNeutralHeader(line []byte, provider string) ([]byte, error) {
	result := append([]byte(nil), line...)
	decoder := json.NewDecoder(bytes.NewReader(result))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if key == "payload" {
			start := int(decoder.InputOffset()) - len(value)
			if err := blankProviderMember(result[start:start+len(value)], provider); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func blankProviderMember(payload []byte, provider string) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if _, err := decoder.Token(); err != nil {
		return err
	}
	for decoder.More() {
		start := int(decoder.InputOffset())
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		if key != "model_provider" {
			continue
		}
		var source string
		if err := json.Unmarshal(value, &source); err != nil {
			return err
		}
		if source == provider && provider != "" {
			return nil
		}
		end := int(decoder.InputOffset())
		next := end
		for next < len(payload) && bytes.ContainsRune([]byte(" \r\n\t"), rune(payload[next])) {
			next++
		}
		if next < len(payload) && payload[next] == ',' {
			// Keep the preceding separator; remove the following separator.
			for start < end && (payload[start] == ',' || bytes.ContainsRune([]byte(" \r\n\t"), rune(payload[start]))) {
				start++
			}
			end = next + 1
		}
		for i := start; i < end; i++ {
			payload[i] = ' '
		}
		return nil
	}
	return nil
}
