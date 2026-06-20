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
	"sync"
	"time"

	"planing_bot/internal/assignee"
	"planing_bot/internal/domain"
	"planing_bot/internal/logging"
	"planing_bot/internal/metrics"
	"planing_bot/internal/parser"
	"planing_bot/internal/service"
)

type Bot struct {
	token       string
	baseURL     string
	botUsername string
	httpClient  *http.Client
	service     *service.Service
	metrics     *metrics.Registry
	logger      *logging.Logger

	mu                    sync.Mutex
	pendingInviteTokens   map[int64]string
	pendingClarifications map[int64]pendingClarification
}

type Option func(*Bot)

type pendingClarification struct {
	Text      string
	Options   []assignee.Option
	CreatedAt time.Time
}

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

func WithBotUsername(username string) Option {
	return func(b *Bot) {
		b.botUsername = strings.TrimPrefix(strings.TrimSpace(username), "@")
	}
}

func New(token string, service *service.Service, opts ...Option) *Bot {
	bot := &Bot{
		token:                 token,
		baseURL:               "https://api.telegram.org/bot" + token,
		httpClient:            &http.Client{Timeout: 60 * time.Second},
		service:               service,
		pendingInviteTokens:   make(map[int64]string),
		pendingClarifications: make(map[int64]pendingClarification),
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
	command, args := splitCommand(text)

	switch command {
	case "/start":
		if _, _, err := b.service.RegisterUser(ctx, user); err != nil {
			return err
		}
		if token, ok := startInviteToken(args); ok {
			b.rememberPendingInvite(user.TelegramID, token)
			return b.sendMessage(ctx, message.Chat.ID, "Инвайт найден. Напиши, как ты будешь называть этого человека в задачах, через запятую.\n\nНапример: Ваня, Иван, сын", nil)
		}
		return b.sendMessage(ctx, message.Chat.ID, startText(), nil)
	case "/help":
		return b.sendMessage(ctx, message.Chat.ID, helpText(), nil)
	case "/add":
		return b.sendMessage(ctx, message.Chat.ID, "Напиши задачу обычным текстом, например: завтра в 18:00 оплатить интернет", nil)
	case "/today":
		tasks, err := b.service.Today(ctx, user)
		if err != nil {
			return err
		}
		return b.sendMessage(ctx, message.Chat.ID, formatTaskList("Сегодня", tasks), nil)
	case "/week":
		tasks, err := b.service.Week(ctx, user)
		if err != nil {
			return err
		}
		return b.sendMessage(ctx, message.Chat.ID, formatTaskList("Ближайшие 7 дней", tasks), nil)
	case "/link", "/invite":
		if args == "" {
			return b.sendMessage(ctx, message.Chat.ID, "Напиши алиасы для человека через запятую: /link мама, мам, Таня", nil)
		}
		result, err := b.service.CreateProfileLinkInvite(ctx, user, splitAliases(args))
		if err != nil {
			return err
		}
		return b.sendMessage(ctx, message.Chat.ID, formatInvite(result, b.botUsername), nil)
	case "/accept":
		token, aliases, ok := parseAcceptArgs(args)
		if !ok {
			return b.sendMessage(ctx, message.Chat.ID, "Формат: /accept <код> Ваня, Иван, сын", nil)
		}
		if _, err := b.service.AcceptProfileLinkInvite(ctx, user, token, splitAliases(aliases)); err != nil {
			return formatProfileLinkError(ctx, b, message.Chat.ID, err)
		}
		b.forgetPendingInvite(user.TelegramID)
		return b.sendMessage(ctx, message.Chat.ID, "Связка профилей активна. Теперь можно ставить задачи друг другу по алиасам.", nil)
	case "/links", "/aliases":
		profiles, err := b.service.LinkedProfiles(ctx, user)
		if err != nil {
			return err
		}
		return b.sendMessage(ctx, message.Chat.ID, formatLinkedProfiles(profiles), nil)
	default:
		if token, ok := b.pendingInvite(user.TelegramID); ok {
			if _, err := b.service.AcceptProfileLinkInvite(ctx, user, token, splitAliases(text)); err != nil {
				return formatProfileLinkError(ctx, b, message.Chat.ID, err)
			}
			b.forgetPendingInvite(user.TelegramID)
			return b.sendMessage(ctx, message.Chat.ID, "Связка профилей активна. Теперь можно ставить задачи друг другу по алиасам.", nil)
		}
		if pending, ok := b.pendingClarification(user.TelegramID); ok {
			return b.handleAssigneeClarification(ctx, message, user, text, pending)
		}
		result, err := b.service.CreateTaskFromText(ctx, user, text)
		if errors.Is(err, parser.ErrEmptyTitle) {
			return b.sendMessage(ctx, message.Chat.ID, "Не понял задачу. Напиши чуть подробнее, что нужно сделать.", nil)
		}
		var clarification *service.AssigneeClarificationError
		if errors.As(err, &clarification) {
			b.rememberClarification(user.TelegramID, pendingClarification{
				Text:      clarification.TaskText,
				Options:   clarification.Options,
				CreatedAt: time.Now(),
			})
			return b.sendMessage(ctx, message.Chat.ID, formatAssigneeClarification(clarification), nil)
		}
		if err != nil {
			return err
		}
		return b.sendMessage(ctx, message.Chat.ID, formatCreatedTask(result), taskKeyboard(result.Task.ID))
	}
}

func (b *Bot) handleAssigneeClarification(ctx context.Context, message message, user domain.TelegramUser, text string, pending pendingClarification) error {
	if isCancelText(text) {
		b.forgetClarification(user.TelegramID)
		return b.sendMessage(ctx, message.Chat.ID, "Ок, не создаю задачу.", nil)
	}
	assigneeUserID, ok := matchClarificationOption(text, pending.Options)
	if !ok {
		return b.sendMessage(ctx, message.Chat.ID, "Не понял, кому поставить. Напиши один из вариантов:\n"+formatAssigneeOptions(pending.Options), nil)
	}
	result, err := b.service.CreateTaskForAssignee(ctx, user, pending.Text, assigneeUserID)
	if errors.Is(err, parser.ErrEmptyTitle) {
		return b.sendMessage(ctx, message.Chat.ID, "Не понял задачу. Напиши чуть подробнее, что нужно сделать.", nil)
	}
	if err != nil {
		return err
	}
	b.forgetClarification(user.TelegramID)
	return b.sendMessage(ctx, message.Chat.ID, formatCreatedTask(result), taskKeyboard(result.Task.ID))
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
		text := "⏰ Задача перенесена: " + task.Title +
			"\nСрок задачи: " + formatOptionalTime(task.DueAt) +
			"\nНапоминание: " + formatOptionalTime(task.RemindAt)
		if task.PostponedCount >= 5 {
			text += fmt.Sprintf("\n\nЗадача переносилась уже %d раз. Возможно, её стоит удалить или разбить.", task.PostponedCount)
		}
		return b.sendMessage(ctx, callback.Message.Chat.ID, text, taskKeyboard(task.ID))
	case "reminder":
		b.incCallback("reminder")
		if len(parts) != 3 {
			return b.answerCallback(ctx, callback.ID, "Не понял перенос напоминания")
		}
		task, err := b.service.PostponeReminder(ctx, user, taskID, parts[2])
		if err != nil {
			return err
		}
		if err := b.answerCallback(ctx, callback.ID, "Напоминание перенесено"); err != nil {
			return err
		}
		text := "🔔 Напоминание перенесено: " + task.Title +
			"\nНапомнить: " + formatOptionalTime(task.RemindAt) +
			"\nСрок задачи: " + formatOptionalTime(task.DueAt)
		return b.sendMessage(ctx, callback.Message.Chat.ID, text, taskKeyboard(task.ID))
	default:
		return b.answerCallback(ctx, callback.ID, "Неизвестное действие")
	}
}

func (b *Bot) rememberPendingInvite(telegramID int64, token string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pendingInviteTokens[telegramID] = token
}

func (b *Bot) pendingInvite(telegramID int64) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	token, ok := b.pendingInviteTokens[telegramID]
	return token, ok
}

func (b *Bot) forgetPendingInvite(telegramID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pendingInviteTokens, telegramID)
}

func (b *Bot) rememberClarification(telegramID int64, pending pendingClarification) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pendingClarifications[telegramID] = pending
}

func (b *Bot) pendingClarification(telegramID int64) (pendingClarification, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending, ok := b.pendingClarifications[telegramID]
	return pending, ok
}

func (b *Bot) forgetClarification(telegramID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pendingClarifications, telegramID)
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
/add — подсказка для добавления
/link мама, Таня — создать инвайт для связки профилей
/accept <код> Ваня, Иван, сын — принять инвайт
/links — показать связанные профили и алиасы`
}

func formatCreatedTask(result *service.CreateTaskResult) string {
	task := result.Task
	category := "—"
	if task.Category != nil {
		category = *task.Category
	}
	assignee := "—"
	if result.Assignee.ID != 0 {
		assignee = formatUserName(result.Assignee)
	}
	return fmt.Sprintf(`✅ Задача создана

Название: %s
Исполнитель: %s
Повтор: %s
Срок задачи: %s
Напоминание: %s
Приоритет: %s
Категория: %s`, task.Title, assignee, formatRecurrence(task.RecurrenceRule), formatOptionalTime(task.DueAt), formatOptionalTime(task.RemindAt), task.Priority, category)
}

func formatInvite(result *service.ProfileLinkInviteResult, botUsername string) string {
	payload := "link_" + result.Token
	link := payload
	if botUsername != "" {
		link = fmt.Sprintf("https://t.me/%s?start=%s", botUsername, payload)
	}
	return fmt.Sprintf("Инвайт создан.\n\nСсылка/код: %s\n\nЯ буду понимать алиасы: %s\n\nПосле открытия ссылки второй человек должен указать, как будет называть тебя.", link, strings.Join(result.Aliases, ", "))
}

func formatLinkedProfiles(profiles []domain.LinkedProfile) string {
	if len(profiles) == 0 {
		return "Связанных профилей пока нет."
	}
	var builder strings.Builder
	builder.WriteString("Связанные профили:")
	for _, profile := range profiles {
		builder.WriteString("\n- ")
		builder.WriteString(formatUserName(profile.User))
		if len(profile.Aliases) > 0 {
			builder.WriteString(" — ")
			builder.WriteString(strings.Join(profile.Aliases, ", "))
		}
	}
	return builder.String()
}

func formatAssigneeClarification(err *service.AssigneeClarificationError) string {
	return fmt.Sprintf("Кому поставить задачу “%s”?\n%s", err.TaskText, formatAssigneeOptions(err.Options))
}

func formatAssigneeOptions(options []assignee.Option) string {
	lines := make([]string, 0, len(options))
	for _, option := range options {
		lines = append(lines, "- "+option.Label)
	}
	return strings.Join(lines, "\n")
}

func formatProfileLinkError(ctx context.Context, b *Bot, chatID int64, err error) error {
	switch {
	case errors.Is(err, service.ErrProfileAliasesEmpty):
		return b.sendMessage(ctx, chatID, "Нужно указать хотя бы один алиас через запятую.", nil)
	case errors.Is(err, service.ErrProfileLinkNotFound):
		return b.sendMessage(ctx, chatID, "Не нашел такой инвайт. Проверь код или попроси создать новый.", nil)
	case errors.Is(err, service.ErrProfileLinkNotPending):
		return b.sendMessage(ctx, chatID, "Этот инвайт уже использован или закрыт.", nil)
	case errors.Is(err, service.ErrProfileLinkSelf):
		return b.sendMessage(ctx, chatID, "Нельзя связать профиль с самим собой.", nil)
	default:
		return err
	}
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
				{"text": "🔔 Через час", "callback_data": fmt.Sprintf("reminder:%d:1h", taskID)},
				{"text": "🔔 Завтра", "callback_data": fmt.Sprintf("reminder:%d:tomorrow", taskID)},
			},
			{
				{"text": "🔔 3 дня", "callback_data": fmt.Sprintf("reminder:%d:3d", taskID)},
				{"text": "🔔 Неделя", "callback_data": fmt.Sprintf("reminder:%d:week", taskID)},
			},
		},
	}
}

func formatUserName(user domain.User) string {
	switch {
	case user.FirstName != "" && user.LastName != "":
		return user.FirstName + " " + user.LastName
	case user.FirstName != "":
		return user.FirstName
	case user.Username != "":
		return "@" + user.Username
	default:
		return fmt.Sprintf("user-%d", user.ID)
	}
}

func splitCommand(text string) (string, string) {
	if !strings.HasPrefix(text, "/") {
		return "", text
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	command := strings.ToLower(fields[0])
	if at := strings.Index(command, "@"); at >= 0 {
		command = command[:at]
	}
	args := ""
	if len(text) > len(fields[0]) {
		args = strings.TrimSpace(text[len(fields[0]):])
	}
	return command, args
}

func splitAliases(text string) []string {
	parts := strings.Split(text, ",")
	aliases := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			aliases = append(aliases, part)
		}
	}
	return aliases
}

func startInviteToken(args string) (string, bool) {
	args = strings.TrimSpace(args)
	if !strings.HasPrefix(args, "link_") {
		return "", false
	}
	return strings.TrimPrefix(args, "link_"), true
}

func parseAcceptArgs(args string) (token string, aliases string, ok bool) {
	fields := strings.Fields(args)
	if len(fields) < 2 {
		return "", "", false
	}
	token = strings.TrimPrefix(fields[0], "link_")
	aliases = strings.TrimSpace(strings.TrimPrefix(args, fields[0]))
	return token, aliases, token != "" && aliases != ""
}

func matchClarificationOption(text string, options []assignee.Option) (int64, bool) {
	var selfID int64
	candidates := make([]assignee.Candidate, 0, len(options))
	for _, option := range options {
		if option.Self {
			selfID = option.UserID
			continue
		}
		candidates = append(candidates, assignee.Candidate{
			UserID:  option.UserID,
			Name:    option.Label,
			Aliases: option.Aliases,
		})
	}
	return assignee.MatchOption(text, selfID, candidates)
}

func isCancelText(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return text == "отмена" || text == "cancel" || text == "не надо"
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
