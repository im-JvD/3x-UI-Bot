package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/robfig/cron/v3"
)

type Config struct {
	PanelURL           string  `json:"panel_url"`
	Username           string  `json:"username"`
	Password           string  `json:"password"`
	TelegramBotToken   string  `json:"telegram_bot_token"`
	AdminChatIDs       []int64 `json:"admin_chat_ids"`
	CronSpec           string  `json:"cron_spec"`
	InsecureSkipVerify bool    `json:"insecure_skip_verify"`
	RequestTimeoutSec  int     `json:"request_timeout_seconds"`
}

type APIClient struct {
	baseURL  string
	username string
	password string
	http     *http.Client
	loggedAt time.Time
}

func NewAPIClient(cfg Config) (*APIClient, error) {
	jar, _ := cookiejar.New(nil)
	to := time.Duration(cfg.RequestTimeoutSec) * time.Second
	if to <= 0 {
		to = 15 * time.Second
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify},
	}
	return &APIClient{
		baseURL:  strings.TrimRight(cfg.PanelURL, "/"),
		username: cfg.Username,
		password: cfg.Password,
		http: &http.Client{
			Timeout:   to,
			Jar:       jar,
			Transport: tr,
		},
	}, nil
}

func (c *APIClient) Login(ctx context.Context) error {
	loginURL := c.baseURL + "/login"
	body := map[string]string{"username": c.username, "password": c.password}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		slurp, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login failed: %s", slurp)
	}
	c.loggedAt = time.Now()
	return nil
}

func (c *APIClient) doJSON(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		if err := c.Login(ctx); err != nil {
			return err
		}
		return c.doJSON(ctx, method, path, payload, out)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slurp, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api failed: %s", slurp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *APIClient) doRaw(ctx context.Context, method, path string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
		return c.doRaw(ctx, method, path)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slurp, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api failed: %s", slurp)
	}
	return io.ReadAll(resp.Body)
}

func (c *APIClient) ServerStatus(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.doJSON(ctx, http.MethodGet, "/panel/api/server/status", nil, &out)
	return out, err
}

func (c *APIClient) OnlineClients(ctx context.Context) ([]string, error) {
	var out []string
	err := c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/onlines", map[string]any{}, &out)
	return out, err
}

func (c *APIClient) Inbounds(ctx context.Context) ([]map[string]any, error) {
	var out []map[string]any
	err := c.doJSON(ctx, http.MethodGet, "/panel/api/inbounds/list", nil, &out)
	return out, err
}

func (c *APIClient) GetDB(ctx context.Context) ([]byte, error) {
	return c.doRaw(ctx, http.MethodGet, "/panel/api/server/getDb")
}

func (c *APIClient) GetConfigJSON(ctx context.Context) ([]byte, error) {
	return c.doRaw(ctx, http.MethodGet, "/panel/api/server/getConfigJson")
}

func mustLoadConfig() Config {
	path := "config.json"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("cannot read config file %s: %v", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		log.Fatalf("invalid config: %v", err)
	}
	return cfg
}

func isAdmin(cfg Config, id int64) bool {
	return slices.Contains(cfg.AdminChatIDs, id)
}

func prettyJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func main() {
	cfg := mustLoadConfig()
	api, _ := NewAPIClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	if err := api.Login(ctx); err != nil {
		log.Fatalf("login failed: %v", err)
	}
	cancel()

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		log.Fatalf("telegram bot failed: %v", err)
	}
	log.Printf("Bot started: @%s", bot.Self.UserName)

	if cfg.CronSpec != "" {
		c := cron.New()
		c.AddFunc(cfg.CronSpec, func() {
			_ = sendPeriodicStatus(bot, api, cfg)
		})
		c.Start()
		defer c.Stop()
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := bot.GetUpdatesChan(u)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-stop:
			log.Println("shutting down...")
			return
		case upd := <-updates:
			if upd.Message == nil {
				continue
			}
			if !isAdmin(cfg, upd.Message.Chat.ID) {
				_, _ = bot.Send(tgbotapi.NewMessage(upd.Message.Chat.ID, "Not authorized."))
				continue
			}
			handleCommand(bot, api, cfg, upd)
		}
	}
}

func handleCommand(bot *tgbotapi.BotAPI, api *APIClient, cfg Config, upd tgbotapi.Update) {
	chatID := upd.Message.Chat.ID
	text := strings.TrimSpace(upd.Message.Text)

	switch {
	case strings.HasPrefix(text, "/start"):
		msg := "✅ 3x-ui Independent Bot\n\nCommands:\n/status\n/online\n/inbounds\n/backup\n/help"
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, msg))

	case strings.HasPrefix(text, "/status"):
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		stat, err := api.ServerStatus(ctx)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "❌ status error: "+err.Error()))
			return
		}
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, "<pre>"+escapeHTML(prettyJSON(stat))+"</pre>"))
	case strings.HasPrefix(text, "/online"):
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		list, err := api.OnlineClients(ctx)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "❌ online error: "+err.Error()))
			return
		}
		if len(list) == 0 {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "No online clients"))
			return
		}
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("🟢 Online: %v", list)))
	case strings.HasPrefix(text, "/inbounds"):
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		inb, err := api.Inbounds(ctx)
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "❌ inbounds error: "+err.Error()))
			return
		}
		var sb strings.Builder
		for _, i := range inb {
			sb.WriteString(fmt.Sprintf("• %v\n", i["remark"]))
		}
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, sb.String()))
	case strings.HasPrefix(text, "/backup"):
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		db, err := api.GetDB(ctx)
		if err == nil {
			_, _ = bot.Send(tgbotapi.NewDocument(chatID, tgbotapi.FileBytes{Name: "x-ui.db", Bytes: db}))
		}
		cfgjson, err := api.GetConfigJSON(ctx)
		if err == nil {
			_, _ = bot.Send(tgbotapi.NewDocument(chatID, tgbotapi.FileBytes{Name: "config.json", Bytes: cfgjson}))
		}
	default:
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Unknown command."))
	}
}

func sendPeriodicStatus(bot *tgbotapi.BotAPI, api *APIClient, cfg Config) error {
	if len(cfg.AdminChatIDs) == 0 {
		return errors.New("no admin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stat, err := api.ServerStatus(ctx)
	if err != nil {
		return err
	}
	msg := tgbotapi.NewMessage(cfg.AdminChatIDs[0], "⏰ Status\n<pre>"+escapeHTML(prettyJSON(stat))+"</pre>")
	msg.ParseMode = "HTML"
	_, _ = bot.Send(msg)
	return nil
}
