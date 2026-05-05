package telegram

// defaultBotCommands is the slash-command menu Telegram surfaces in:
//   - the `/` autocomplete inside the chat
//   - the small "Menu" button next to the text input
//
// Order here is the order the user sees. Keep descriptions short and friendly
// (Telegram trims long ones).
var defaultBotCommands = []BotCommand{
	{Command: "today", Description: "Today's spending"},
	{Command: "week", Description: "This week's spending"},
	{Command: "month", Description: "This month's spending"},
	{Command: "year", Description: "This year's spending"},
	{Command: "last", Description: "Show last N expenses (default 5)"},
	{Command: "undo", Description: "Delete the most recent expense"},
	{Command: "ledgers", Description: "List your ledgers"},
	{Command: "ledger", Description: "Set, clear, or add a ledger"},
	{Command: "categories", Description: "List your categories"},
	{Command: "category", Description: "Add a new category"},
	{Command: "help", Description: "Show help & examples"},
}

// defaultBotDescription is the long welcome text shown on the bot's
// chat-open screen before any messages exist.
const defaultBotDescription = `Pennywise — log expenses by texting in plain language.

Try:
• 5000 fuel
• 12.50 coffee
• 30 groceries yesterday

Type / to see all commands.`

// defaultBotShortDescription appears in Telegram search results.
const defaultBotShortDescription = "Personal expense tracker — Pennywise"
