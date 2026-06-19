package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/logging"
	"planing_bot/internal/metrics"
	"planing_bot/internal/parser"
	"planing_bot/internal/service"
)

type Bot struct {
	token      string
	baseURL    string
	httpClient *http.Client
	service    *service.Service
	metrics    *metrics.Registry
	logger     *logging.Logger
}

type Option func(*Bot)

func WithMetrics(registry *metrics.Registry) Option {
	return func(b *Bot) {
		b.metrics = registry
	}
}

func WithLogger(logger *logging.Logger) Option {
	return func(b *Bot) {
		b.logger = logger
	}
}

func New(token string, service *service.Service, opts ...Option) *Bot {
	bot := &Bot{
		token:      token,
		baseURL:    "https://api.telegram.org/bot" + token,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		service:    service,
	}
	for _, opt := range opts {
		opt(bot)
	}
	return bot
}

func (b *Bot) Run(ctx context.Context) error {
	if b.token == "" {
		return errors.New("BOT_TOKEN is empty")
	}
	var offset int64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		updates, err := b.getUpdates(ctx, offset)
		if err != nil {
			b.recordTelegramSendError("get_updates", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			if err := b.handleUpdate(ctx, update); err != nil {
				b.logError("telegram_update_failed", err, logging.Fields{"update_id": update.UpdateID})
			}
		}
	}
}

func (b *Bot) SendChatMessage(ctx context.Context, telegramID int64, text string) error {
	return b.sendMessage(ctx, telegramID, text, nil)
}

func (b *Bot) handleUpdate(ctx context.Context, update update) error {
	start := time.Now()
	updateType := updateType(update)
	if b.metrics != nil {
		b.metrics.Inc("telegram_update_total", metrics.Labels{"type": updateType})
	}
	defer func() {
		if b.metrics != nil {
			b.metrics.ObserveDuration("telegram_update_duration_seconds", nil, start)
		}
	}()
	if update.Message != nil {
		return b.handleMessage(ctx, *update.Message)
	}
	if update.CallbackQuery != nil {
		return b.handleCallback(ctx, *update.CallbackQuery)
	}
	return nil
}

func (b *Bot) handleMessage(ctx context.Context, message message) error {
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return nil
	}
	user := telegramUser(message.From)

	switch {
	case strings.HasPrefix(text, "/start"):
		if _, _, err := b.service.RegisterUser(ctx, user); err != nil {
			return err
		}
		return b.sendMessage(ctx, message.Chat.ID, startText(), nil)
	case strings.HasPrefix(text, "/help"):
		return b.sendMessage(ctx, message.Chat.ID, helpText(), nil)
	case strings.HasPrefix(text, "/add"):
		return b.sendMessage(ctx, message.Chat.ID, "Напиши задачу обычным текстом, например: завтра в 18:00 оплатить интернет", nil)
	case strings.HasPrefix(text, "/today"):
		tasks, err := b.service.Today(ctx, user)
		if err != nil {
			return err
		}
		return b.sendMessage(ctx, message.Chat.ID, formatTaskList("Сегодня", tasks), nil)
	case strings.HasPrefix(text, "/week"):
		tasks, err := b.service.Week(ctx, user)
		if err != nil {
			return err
		}
		return b.sendMessage(ctx, message.Chat.ID, formatTaskList("Ближайшие 7 дней", tasks), nil)
	default:
		result, err := b.service.CreateTaskFromText(ctx, user, text)
		if errors.Is(err, parser.ErrEmptyTitle) {
			return b.sendMessage(ctx, message.Chat.ID, "Не понял задачу. Напиши чуть подробнее, что нужно сделать.", nil)
		}
		if err != nil {
			return err
		}
		return b.sendMessage(ctx, message.Chat.ID, formatCreatedTask(result), taskKeyboard(result.Task.ID))
	}
}

func (b *Bot) handleCallback(ctx context.Context, callback callbackQuery) error {
	parts := strings.Split(callback.Data, ":")
	if len(parts) < 2 {
		return b.answerCallback(ctx, callback.ID, "Не понял действие")
	}
	taskID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return b.answerCallback(ctx, callback.ID, "Не понял задачу")
	}
	user := telegramUser(callback.From)

	switch parts[0] {
	case "done":
		b.incCallback("done")
		task, err := b.service.MarkDone(ctx, user, taskID)
		if err != nil {
			return err
		}
		if err := b.answerCallback(ctx, callback.ID, "Готово"); err != nil {
			return err
		}
		return b.sendMessage(ctx, callback.Message.Chat.ID, "✅ Готово: "+task.Title, nil)
	case "cancel":
		b.incCallback("cancel")
		task, err := b.service.Cancel(ctx, user, taskID)
		if err != nil {
			return err
		}
		if err := b.answerCallback(ctx, callback.ID, "Отменено"); err != nil {
			return err
		}
		return b.sendMessage(ctx, callback.Message.Chat.ID, "❌ Отменено: "+task.Title, nil)
	case "postpone":
		b.incCallback("postpone")
		if len(parts) != 3 {
			return b.answerCallback(ctx, callback.ID, "Не понял перенос")
		}
		task, err := b.service.Postpone(ctx, user, taskID, parts[2])
		if err != nil {
			return err
		}
		if err := b.answerCallback(ctx, callback.ID, "Перенесено"); err != nil {
			return err
		}
		text := "⏰ Перенесено: " + task.Title + "\nСрок: " + formatOptionalTime(task.DueAt)
		if task.PostponedCount >= 5 {
			text += fmt.Sprintf("\n\nЗадача переносилась уже %d раз. Возможно, её стоит удалить или разбить.", task.PostponedCount)
		}
		return b.sendMessage(ctx, callback.Message.Chat.ID, text, taskKeyboard(task.ID))
	default:
		return b.answerCallback(ctx, callback.ID, "Неизвестное действие")
	}
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	url := fmt.Sprintf("%s/getUpdates?timeout=50&offset=%d", b.baseURL, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp updatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}
	if !apiResp.OK {
		return nil, fmt.Errorf("telegram getUpdates failed: %s", apiResp.Description)
	}
	return apiResp.Result, nil
}

func (b *Bot) sendMessage(ctx context.Context, chatID int64, text string, replyMarkup any) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	return b.postJSON(ctx, "sendMessage", payload, nil)
}

func (b *Bot) answerCallback(ctx context.Context, callbackID string, text string) error {
	payload := map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	}
	return b.postJSON(ctx, "answerCallbackQuery", payload, nil)
}

func (b *Bot) postJSON(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		b.recordTelegramSendError(method, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		err := fmt.Errorf("telegram %s status: %s", method, resp.Status)
		b.recordTelegramSendError(method, err)
		return err
	}
	if out == nil {
		var apiResp struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			b.recordTelegramSendError(method, err)
			return err
		}
		if !apiResp.OK {
			err := fmt.Errorf("telegram %s failed: %s", method, apiResp.Description)
			b.recordTelegramSendError(method, err)
			return err
		}
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		b.recordTelegramSendError(method, err)
		return err
	}
	return nil
}

func telegramUser(user user) domain.TelegramUser {
	return domain.TelegramUser{
		TelegramID: user.ID,
		Username:   user.Username,
		FirstName:  user.FirstName,
		LastName:   user.LastName,
	}
}

func (b *Bot) incCallback(action string) {
	if b.metrics != nil {
		b.metrics.Inc("telegram_callback_total", metrics.Labels{"action": action})
	}
}

func (b *Bot) recordTelegramSendError(operation string, err error) {
	if b.metrics != nil {
		b.metrics.Inc("telegram_send_error_total", metrics.Labels{"operation": operation})
	}
	b.logError("telegram_send_failed", err, logging.Fields{"operation": operation})
}

func (b *Bot) logError(event string, err error, fields logging.Fields) {
	if b.logger != nil {
		b.logger.Error(event, err, fields)
	}
}

func updateType(update update) string {
	switch {
	case update.Message != nil:
		return "message"
	case update.CallbackQuery != nil:
		return "callback"
	default:
		return "unknown"
	}
}

func startText() string {
	return `Привет! Я помогу вести список дел.

Пиши задачу обычным текстом:
завтра в 18:00 оплатить интернет
срочно отправить документ
когда-нибудь посмотреть PostgreSQL internals

Команды: /today, /week, /add, /help`
}

func helpText() string {
	return `Я понимаю даты, время, приоритеты и простые категории.

Примеры:
завтра купить корм
сегодня в 18:00 полить огурцы
через 3 дня оплатить интернет
25 июня в 12:00 врач

Команды:
/today — задачи на сегодня
/week — задачи на 7 дней
/add — подсказка для добавления`
}

func formatCreatedTask(result *service.CreateTaskResult) string {
	task := result.Task
	category := "—"
	if task.Category != nil {
		category = *task.Category
	}
	return fmt.Sprintf(`✅ Задача создана

Название: %s
Повтор: %s
Срок: %s
Напомнить: %s
Приоритет: %s
Категория: %s`, task.Title, formatRecurrence(task.RecurrenceRule), formatOptionalTime(task.DueAt), formatOptionalTime(task.RemindAt), task.Priority, category)
}

func formatTaskList(title string, tasks []domain.Task) string {
	if len(tasks) == 0 {
		return title + "\n\nЗадач нет."
	}
	groups := map[domain.Priority][]domain.Task{
		domain.PriorityP1: {},
		domain.PriorityP2: {},
		domain.PriorityP3: {},
		domain.PriorityP4: {},
	}
	for _, task := range tasks {
		groups[task.Priority] = append(groups[task.Priority], task)
	}

	var builder strings.Builder
	builder.WriteString(title)
	for _, priority := range []domain.Priority{domain.PriorityP1, domain.PriorityP2, domain.PriorityP3, domain.PriorityP4} {
		items := groups[priority]
		if len(items) == 0 {
			continue
		}
		builder.WriteString("\n\n")
		builder.WriteString(strings.ToUpper(string(priority)))
		for _, task := range items {
			builder.WriteString("\n- ")
			builder.WriteString(task.Title)
			if task.DueAt != nil {
				builder.WriteString(" — ")
				builder.WriteString(task.DueAt.Format("02.01 15:04"))
			}
		}
	}
	return builder.String()
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Format("02.01.2006 15:04")
}

func formatRecurrence(rule *domain.RecurrenceRule) string {
	if rule == nil {
		return "—"
	}
	switch *rule {
	case domain.RecurrenceDaily:
		return "каждый день"
	default:
		return string(*rule)
	}
}

func taskKeyboard(taskID int64) map[string]any {
	return map[string]any{
		"inline_keyboard": [][]map[string]string{
			{
				{"text": "✅ Готово", "callback_data": fmt.Sprintf("done:%d", taskID)},
				{"text": "❌ Отменить", "callback_data": fmt.Sprintf("cancel:%d", taskID)},
			},
			{
				{"text": "⏰ Завтра", "callback_data": fmt.Sprintf("postpone:%d:tomorrow", taskID)},
				{"text": "⏰ 3 дня", "callback_data": fmt.Sprintf("postpone:%d:3d", taskID)},
				{"text": "⏰ Неделя", "callback_data": fmt.Sprintf("postpone:%d:week", taskID)},
			},
		},
	}
}

type updatesResponse struct {
	OK          bool     `json:"ok"`
	Description string   `json:"description"`
	Result      []update `json:"result"`
}

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	From      user   `json:"from"`
	Chat      chat   `json:"chat"`
	Text      string `json:"text"`
}

type callbackQuery struct {
	ID      string  `json:"id"`
	From    user    `json:"from"`
	Message message `json:"message"`
	Data    string  `json:"data"`
}

type user struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type chat struct {
	ID int64 `json:"id"`
}
