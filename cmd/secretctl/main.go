// secretctl 将 API Key 加密成可手工执行的 PostgreSQL UPSERT SQL。
// 它不连接数据库、不保存明文，也不提供解密/显示密钥功能。
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var allowedKeys = map[string]struct{}{
	"llm.api_key":             {},
	"asr.api_key":             {},
	"asr.aliyun.api_key":      {},
	"asr.siliconflow.api_key": {},
	"tts.api_key":             {},
	"tts.aliyun.api_key":      {},
}

func main() {
	key := flag.String("key", "", "encrypted_secrets 中的 secret_key")
	masterKeyPath := flag.String("master-key", "data/swallow.master.key", "base64 主密钥文件")
	flag.Parse()
	if _, ok := allowedKeys[*key]; !ok {
		fatalf("unsupported -key %q", *key)
	}

	masterKey, err := readMasterKey(*masterKeyPath)
	if err != nil {
		fatalf("read master key: %v", err)
	}
	plaintext, err := readSecret()
	if err != nil {
		fatalf("read API key: %v", err)
	}
	defer clearBytes(plaintext)
	if len(plaintext) == 0 {
		fatalf("API key must not be empty")
	}

	ciphertext, nonce, err := encrypt(masterKey, *key, plaintext)
	clearBytes(masterKey)
	if err != nil {
		fatalf("encrypt API key: %v", err)
	}
	fmt.Printf("INSERT INTO encrypted_secrets (secret_key, ciphertext, nonce, algorithm, key_version, updated_at)\n")
	fmt.Printf("VALUES ('%s', decode('%s','hex'), decode('%s','hex'), 'aes-256-gcm', 1, CURRENT_TIMESTAMP)\n", *key, hex.EncodeToString(ciphertext), hex.EncodeToString(nonce))
	fmt.Printf("ON CONFLICT (secret_key) DO UPDATE SET ciphertext=EXCLUDED.ciphertext, nonce=EXCLUDED.nonce, algorithm=EXCLUDED.algorithm, key_version=EXCLUDED.key_version, updated_at=EXCLUDED.updated_at;\n")
}

func readMasterKey(path string) ([]byte, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must decode to 32 bytes")
	}
	return key, nil
}

func readSecret() ([]byte, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "API Key（输入不会回显）: ")
		value, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return value, err
	}
	value, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024))
	return []byte(strings.TrimSpace(string(value))), err
}

func encrypt(masterKey []byte, key string, plaintext []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, []byte(key)), nonce, nil
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "secretctl: "+format+"\n", args...)
	os.Exit(1)
}
