// Package i18n provides human-facing UI/CLI message localization. It
// intentionally covers only text shown to the user; LLM-facing content (system
// prompt, tool schemas, tool results) stays in English.
package i18n

import (
	"fmt"
	"strings"
)

// Lang identifies a supported language.
type Lang string

const (
	En Lang = "en"
	Ru Lang = "ru"
)

// current holds the active language, set once at startup.
var current Lang = En

// Set switches the active language.
func Set(l Lang) { current = l }

// Get returns the active language.
func Get() Lang { return current }

// Parse maps a config/flag/env value to a Lang. "ru"/"русский" select Russian;
// anything else (including "") falls back to English.
func Parse(s string) Lang {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ru", "рус", "русский", "russian":
		return Ru
	default:
		return En
	}
}

// catalog maps message keys to their translations. Keys are namespaced by
// package/area: main, agent, plan, compact, session, help, llm, perm, tui,
// edit.
var catalog = map[Lang]map[string]string{
	En: {
		"main.config_error":         "config error: %v",
		"main.tui_error":            "tui error: %v",
		"main.agent_error":          "agent error: %v",
		"main.no_session":           "  [no session to resume]\n",
		"main.version":              "tiny-code %s",
		"agent.interrupted":         "\n  [interrupted by user]\n",
		"agent.no_response":         "\n  [model did not respond, stopping]\n",
		"agent.empty_reply":         "  [%d/%d] (empty reply, retrying without thinking)",
		"agent.too_many_calls":      "  [%d tool calls in one reply, keeping the first %d]",
		"agent.tool_line":           "  [%d/%d tool: %s(%s)]",
		"agent.tool_error":          "  !!! %s",
		"agent.tool_result":         "  -> result (%dc): %s",
		"agent.repeating_tool":      "  [repeating same tool, recovery prompt]",
		"agent.finished_repeat":     "  [task finished: model repeated the answer]\n",
		"agent.round":               "  [%d/%d] %s",
		"agent.repeating_answer":    "  [model repeating same answer, stopping]\n",
		"agent.text_twice":          "  [model answered in text twice, treating as done]\n",
		"agent.stuck_reasoning":     "\n  [model stuck in reasoning loop, stopping]\n",
		"agent.max_rounds":          "\n  [max rounds reached, summarizing work...]",
		"agent.asks":                "--- AI asks: %s ---",
		"agent.asks_prompt":         "> ",
		"agent.resumed":             "  [resumed session: %s (%d messages)]\n",
		"agent.path_corrected":      "  [path corrected: %q -> %q]",
		"agent.command_normalized":  "  [command normalized: %s]",
		"agent.path_quoted":         "  [path quoted: %s]",
		"agent.running_cmd":         "  [running command…]",
		"agent.banner_model":        "  tiny-code — model: %s",
		"agent.banner_workspace":    "  workspace: %s",
		"agent.banner_permissions":  "  permissions: %s",
		"agent.banner_help":         "  Type /help for commands. Ctrl+C to exit.\n",
		"agent.input_prompt":        ">>> ",
		"agent.bye":                 "bye!",
		"agent.unknown_cmd":         "  [unknown command: %s — type /help]",
		"plan.analyzing":            "  [analyzing and creating plan...]",
		"plan.no_response":          "  [model did not respond]\n",
		"plan.no_plan":              "  [model did not create a plan]\n",
		"plan.steps":                "  [plan has %d steps, allocating up to %d rounds]",
		"plan.title":                "  PLAN",
		"plan.none":                 "  [no plan to approve - try /plan again]\n",
		"plan.approve_prompt":       "[Plan ready. Approve and execute? (y/n/edit)] ",
		"plan.edit":                 "  [Edit the plan and say 'continue']\n",
		"plan.rejected":             "  [Plan rejected. Type /plan again or give feedback.]\n",
		"compact.nothing":           "  [nothing to compact]\n",
		"compact.done":              "  [context compacted: model summary]\n",
		"session.save_failed":       "  [session save failed: %v]\n",
		"session.saved":             "  [session saved: %s]\n",
		"session.none_to_resume":    "  [no sessions to resume]\n",
		"session.not_found":         "  [session '%s' not found]\n",
		"session.resumed":           "  [resumed session: %s (%d messages)]\n",
		"session.usage":             "  Usage: /session save [name] | /session load [name] | /session list\n",
		"session.list_empty":        "  [no saved sessions]\n",
		"session.list_header_name":  "Name",
		"session.list_header_model": "Model",
		"session.list_header_msgs":  "Msgs",
		"session.list_header_time":  "Time",
		"session.cleared":           "  [context cleared, fresh start]\n",
		"session.new":               "  [new session — previous saved, context cleared]\n",
		"btw.usage":                 "  Usage: /btw <question>\n",
		"btw.no_answer":             "  [/btw] no answer\n",
		"btw.result":                "  [/btw]\n%s\n",
		"help.text": `Commands:
  /help                 Show this help
  /session save [name]  Save the current session
  /session load [name]  Load a session (last one if no name given)
  /session list         List saved sessions
  /sessions             Same as /session list
  /clear                Clear conversation, start fresh
  /new                  Start a new session (previous one is saved)
  /plan [desc]          Enter plan mode (analyze first, then act)
  /compact              Summarize and shrink context
  /btw <question>       Ask a side question without interrupting the agent
  /exit                 End session

  ! <command>           Run a shell command directly

  Ctrl+C to interrupt.
`,
		"llm.thinking":        "  [#%d thinking…]",
		"llm.stalled":         "\n  [Model stalled (90s timeout). Forcing continue.]",
		"llm.rate_limited":    "\n  [Error: Rate limited. Wait and try again.]",
		"llm.error":           "\n  [Error: %s]",
		"llm.cant_connect":    "\n  [Error: Can't connect to the model server at http://%s:%d]",
		"llm.port_hint":       "  [Make sure the server is running on port %d]\n",
		"llm.spinner_default": "waiting for model…",
		"llm.thinking_toks":   "  [#%d thinking: %d tok • %.0fs]",
		"llm.answering_toks":  "  [#%d answering: %d tok • %.0fs]",
		"llm.preparing_tool":  "  [#%d preparing tool call…]",
		"llm.stream_error":    "\n  [stream error: %v]",
		"llm.repetition":      "\n  [repetition detected, response cut]",
		"llm.summary_tool":    "\r  [#%d tool call • %s • %.0fs]",
		"llm.summary_model":   "\r  [#%d model • %s • %.0fs]",
		"llm.summary_empty":   "\r  [#%d empty • %.0fs]",
		"llm.tok_chars":       "%d chars",
		"llm.tok_tokens":      "%d tok",
		"perm.run":            "Run this command?\n  $ %s\nApprove? (y/n/a always) ",
		"perm.write":          "Write to %s? (y/n) ",
		"tui.loading":         "loading…",
		"tui.subtitle":        " — local AI agent",
		"tui.model_info":      " · model %s · %s",
		"tui.status_label":    " status",
		"tui.files":           " files",
		"tui.files_active":    " files [active]",
		"tui.footer":          "Tab — files · Enter — send · Esc — stop · ↑/↓ — scroll · Ctrl+C — quit",
		"tui.esc_stopped":     "[Esc] generation stopped — type a new request or continue.",
		"tui.cmd_unknown":     "unknown command: %s",
		"tui.btw_hint":        "[/btw] — type: /btw <question>",
		"tui.btw_answering":   "→ answering /btw…",
		"tui.btw_no_answer":   "  [/btw] no answer",
		"tui.btw_input":       "/btw — type a question (Enter — send, Esc — cancel)",
		"tui.agent_busy":      "Agent is working… type /btw <question> to ask a side question",
		"tui.empty":           "(empty)",
		"tui.model_status":    "model: %s\nworkspace: %s\nperms: %s",
		"edit.both_given":     "[edit_file: start_line and old_string both given for %s; using line numbers]",
		"update.available":    "\n  Update available: %s (current: %s)\n  Run `tiny-code -update` to install.\n",
		"update.downloading":  "  Downloading %s…",
		"update.done":         "  Updated to %s. Restart tiny-code to use the new version.\n",
		"update.failed":       "  Update failed: %v\n",
	},
	Ru: {
		"main.config_error":         "ошибка конфигурации: %v",
		"main.tui_error":            "ошибка интерфейса: %v",
		"main.agent_error":          "ошибка агента: %v",
		"main.no_session":           "  [нет сессии для восстановления]\n",
		"main.version":              "tiny-code %s",
		"agent.interrupted":         "\n  [прервано пользователем]\n",
		"agent.no_response":         "\n  [модель не ответила, остановка]\n",
		"agent.empty_reply":         "  [%d/%d] (пустой ответ, повтор без размышлений)",
		"agent.too_many_calls":      "  [%d вызовов инструментов за раз, оставляю первые %d]",
		"agent.tool_line":           "  [%d/%d инструмент: %s(%s)]",
		"agent.tool_error":          "  !!! %s",
		"agent.tool_result":         "  -> результат (%dс): %s",
		"agent.repeating_tool":      "  [повтор одного и того же инструмента, восстановление]",
		"agent.finished_repeat":     "  [задача завершена: модель повторила ответ]\n",
		"agent.round":               "  [%d/%d] %s",
		"agent.repeating_answer":    "  [модель повторяет один и тот же ответ, остановка]\n",
		"agent.text_twice":          "  [модель дважды ответила текстом, считаю выполненным]\n",
		"agent.stuck_reasoning":     "\n  [модель зациклилась на рассуждениях, остановка]\n",
		"agent.max_rounds":          "\n  [достигнут лимит раундов, подвожу итог...]",
		"agent.asks":                "--- ИИ спрашивает: %s ---",
		"agent.asks_prompt":         "> ",
		"agent.resumed":             "  [сессия восстановлена: %s (%d сообщений)]\n",
		"agent.path_corrected":      "  [путь исправлен: %q -> %q]",
		"agent.command_normalized":  "  [команда нормализована: %s]",
		"agent.path_quoted":         "  [путь в кавычках: %s]",
		"agent.running_cmd":         "  [команда выполняется…]",
		"agent.banner_model":        "  tiny-code — модель: %s",
		"agent.banner_workspace":    "  рабочая папка: %s",
		"agent.banner_permissions":  "  права доступа: %s",
		"agent.banner_help":         "  Введите /help для списка команд. Ctrl+C — выход.\n",
		"agent.input_prompt":        ">>> ",
		"agent.bye":                 "пока!",
		"agent.unknown_cmd":         "  [неизвестная команда: %s — введите /help]",
		"plan.analyzing":            "  [анализирую и составляю план...]",
		"plan.no_response":          "  [модель не ответила]\n",
		"plan.no_plan":              "  [модель не составила план]\n",
		"plan.steps":                "  [в плане %d шагов, выделяю до %d раундов]",
		"plan.title":                "  ПЛАН",
		"plan.none":                 "  [нет плана для утверждения — попробуйте /plan ещё раз]\n",
		"plan.approve_prompt":       "[План готов. Утвердить и выполнить? (y/n/edit)] ",
		"plan.edit":                 "  [Отредактируйте план и введите 'continue']\n",
		"plan.rejected":             "  [План отклонён. Введите /plan ещё раз или оставьте комментарий.]\n",
		"compact.nothing":           "  [нечего сжимать]\n",
		"compact.done":              "  [контекст сжат: краткое изложение от модели]\n",
		"session.save_failed":       "  [не удалось сохранить сессию: %v]\n",
		"session.saved":             "  [сессия сохранена: %s]\n",
		"session.none_to_resume":    "  [нет сессий для восстановления]\n",
		"session.not_found":         "  [сессия '%s' не найдена]\n",
		"session.resumed":           "  [сессия восстановлена: %s (%d сообщений)]\n",
		"session.usage":             "  Использование: /session save [имя] | /session load [имя] | /session list\n",
		"session.list_empty":        "  [нет сохранённых сессий]\n",
		"session.list_header_name":  "Имя",
		"session.list_header_model": "Модель",
		"session.list_header_msgs":  "Сообщ.",
		"session.list_header_time":  "Время",
		"session.cleared":           "  [контекст очищен, новый старт]\n",
		"session.new":               "  [новая сессия — предыдущая сохранена, контекст очищен]\n",
		"btw.usage":                 "  Использование: /btw <вопрос>\n",
		"btw.no_answer":             "  [/btw] нет ответа\n",
		"btw.result":                "  [/btw]\n%s\n",
		"help.text": `Команды:
  /help                 Показать эту справку
  /session save [имя]  Сохранить текущую сессию
  /session load [имя]  Загрузить сессию (последнюю, если имя не указано)
  /session list         Список сохранённых сессий
  /sessions             То же, что /session list
  /clear                Очистить разговор, начать заново
  /new                  Начать новую сессию (предыдущая сохраняется)
  /plan [описание]      Режим планирования (сначала анализ, потом действия)
  /compact              Сжать и сократить контекст
  /btw <вопрос>         Задать побочный вопрос, не прерывая агента
  /exit                 Завершить сессию

  ! <команда>           Выполнить команду оболочки напрямую

  Ctrl+C — прервать.
`,
		"llm.thinking":        "  [#%d модель думает…]",
		"llm.stalled":         "\n  [Модель зависла (таймаут 90с). Принудительно продолжаю.]",
		"llm.rate_limited":    "\n  [Ошибка: превышен лимит запросов. Подождите и попробуйте снова.]",
		"llm.error":           "\n  [Ошибка: %s]",
		"llm.cant_connect":    "\n  [Ошибка: не удалось подключиться к серверу модели http://%s:%d]",
		"llm.port_hint":       "  [Проверьте, что сервер запущен на порту %d]\n",
		"llm.spinner_default": "жду ответа модели…",
		"llm.thinking_toks":   "  [#%d модель думает: %d ток • %.0fс]",
		"llm.answering_toks":  "  [#%d модель отвечает: %d ток • %.0fс]",
		"llm.preparing_tool":  "  [#%d модель готовит вызов инструмента…]",
		"llm.stream_error":    "\n  [ошибка потока: %v]",
		"llm.repetition":      "\n  [обнаружен повтор, ответ обрезан]",
		"llm.summary_tool":    "\r  [#%d вызов инструмента • %s • %.0fс]",
		"llm.summary_model":   "\r  [#%d модель • %s • %.0fс]",
		"llm.summary_empty":   "\r  [#%d пусто • %.0fс]",
		"llm.tok_chars":       "%d симв",
		"llm.tok_tokens":      "%d ток",
		"perm.run":            "Выполнить эту команду?\n  $ %s\nПодтвердить? (y/n/a всегда) ",
		"perm.write":          "Записать в %s? (y/n) ",
		"tui.loading":         "загрузка…",
		"tui.subtitle":        " — локальный AI-агент",
		"tui.model_info":      " · модель %s · %s",
		"tui.status_label":    " статус",
		"tui.files":           " файлы",
		"tui.files_active":    " файлы [активно]",
		"tui.footer":          "Tab — файлы · Enter — отправить · Esc — остановить · ↑/↓ — скролл · Ctrl+C — выход",
		"tui.esc_stopped":     "[Esc] генерация остановлена — введите новый запрос или продолжите.",
		"tui.cmd_unknown":     "команда не известна: %s",
		"tui.btw_hint":        "[/btw] — введите: /btw <вопрос>",
		"tui.btw_answering":   "→ отвечаю на /btw…",
		"tui.btw_no_answer":   "  [/btw] нет ответа",
		"tui.btw_input":       "/btw — введите вопрос (Enter — отправить, Esc — отмена)",
		"tui.agent_busy":      "Агент работает… начните вводить /btw <вопрос>, чтобы спросить мимоходом",
		"tui.empty":           "(пусто)",
		"tui.model_status":    "модель: %s\nрабочая папка: %s\nправа: %s",
		"edit.both_given":     "[edit_file: заданы и start_line, и old_string для %s; использую номера строк]",
		"update.available":    "\n  Доступно обновление: %s (текущая: %s)\n  Запустите `tiny-code -update` для установки.\n",
		"update.downloading":  "  Скачиваю %s…",
		"update.done":         "  Обновлено до %s. Перезапустите tiny-code.\n",
		"update.failed":       "  Ошибка обновления: %v\n",
	},
}

// T returns the localized message for key. A missing key falls back to the key
// itself; args are applied via fmt.Sprintf when present.
func T(key string, args ...any) string {
	msg, ok := catalog[current][key]
	if !ok {
		msg = key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}
