# Telegram bot setup

Pennywise's optional Telegram bot lets you log expenses by texting in plain
language, ask questions like *"how much this month?"*, or send a photo of a
receipt. Pennywise is **BYOB — bring your own bot**: you create the bot via
@BotFather, paste the token in Pennywise, and traffic goes directly between
your install and Telegram. There's no Pennywise-hosted relay.

Setup takes about three minutes.

## 1. Create the bot via BotFather

1. Open Telegram, search for `@BotFather`, start a chat.
2. Send `/newbot`. BotFather asks for a display name (any string) and a
   username (must end in `bot`, e.g. `my_pennywise_bot`).
3. BotFather replies with a token — a long string like
   `1234567890:ABCdefGHIjklMNOpqrsTUVwxyz0123456789`. Keep it secret; this is
   the equivalent of a password for your bot.

Optional: send `/setdescription`, `/setabouttext`, or `/setuserpic` to
customize how the bot appears.

## 2. Save the token in Pennywise

1. Open *Settings → Telegram Bot* in Pennywise.
2. Paste the token. Click **Save**.

Pennywise validates the token immediately by calling Telegram's `getMe`. On
success the bot's username is shown back to you. Polling starts automatically
so the bot can RECEIVE the pairing message in the next step.

The token is encrypted at rest with AES-GCM, using the same session-secret-
derived key as the LLM API key.

## 3. Pair your Telegram chat to this Pennywise install

The pairing step proves to Pennywise that the chat sending messages to the bot
belongs to the install's owner — not just anyone who happened to find the
bot's username.

1. In *Settings → Telegram Bot*, click **Generate pairing code**. You get a
   six-character code with a `PW-` prefix (e.g. `PW-A4B7XQ`). The code expires
   in 10 minutes.
2. In Telegram, find your bot (the username from step 1). Send `/start PW-A4B7XQ`
   (or `/start PW-a4b7xq` — case-insensitive).
3. The bot replies with **"✅ Pairing successful."** and a list of example
   commands. Pennywise has now stored your chat ID; only this chat can talk
   to the bot from now on.

If the wrong code is sent, the bot replies "Invalid or expired pairing code."
You can generate a new code as many times as you like.

## 4. (Recommended) Configure your LLM provider first

The bot uses an LLM to parse free-text expenses. If no provider is configured,
the bot replies with a setup hint instead of a confirmation. See
[`llm-providers.md`](llm-providers.md) for the four supported providers.

## What the bot can do

### Log expenses by texting

The simplest case — the bot parses a free-text message into an expense and
shows a confirmation:

- *5000 fuel*
- *12.50 coffee yesterday*
- *30 groceries*

Confirmation messages have inline buttons for **Edit**, **Delete**, **Category…**,
and **Ledger…**.

### Multi-line batch entry

Send several lines at once (one per expense) and the bot logs them as a batch:

```
15 lunch
40 groceries
8 coffee
```

The reply is a summary with an **↩ Undo all** button that soft-deletes the
batch in one tap.

### Ask questions

- *how much this month?*
- *last 5 expenses*
- *what have I spent on the trip?*

Or use the slash commands:

- `/today`, `/week`, `/month`, `/year` — totals for the period
- `/last [n]` — last N expenses (default 5, max 20)
- `/undo` — soft-delete the most recent expense
- `/ledgers` — list ledgers
- `/ledger <name>` — set a sticky ledger context (next expenses go there)
- `/ledger off` — clear the sticky context
- `/ledger new <name>` — create a ledger
- `/categories` — list categories
- `/category new <name>` — create a category
- `/cancel` — abort an in-progress action
- `/help` — show the full reference, including your dashboard URL

### Send a receipt photo

Snap a photo of a receipt and send it to the bot. Pennywise downloads the
image, sends it to your vision-capable LLM, and shows a confirmation with the
parsed total + merchant. Works with photos and documents (JPG, PNG, WebP,
HEIC, PDF — provider-dependent; see [`receipts.md`](receipts.md)).

You can include a caption with the photo to refine the date or category:
*"this was yesterday"*. The amount and merchant always come from the image.

### Low-confidence prompts

When the LLM isn't sure (e.g. *"paid 3000"* with no clear category), the bot
asks before logging:

```
❓ Log this? I'm not 100% sure I read this right.

$3,000.00 — Paid
📅 Today · 🏷 Other
Confidence: 55%

[ ✓ Yes, log it ]  [ ✏️ Edit ]  [ ✕ Cancel ]
```

- **Yes** → expense is logged.
- **Edit** → the bot waits for a corrected message; after one re-prompt cycle
  it will commit even if confidence is still low (avoids ping-pong loops).
- **Cancel** → discarded, no DB row.

The threshold is 0.7. Above that, expenses log silently like before. Batch
parses always commit (with the **Undo all** escape hatch); the per-item
prompt only fires for single-expense messages.

## Troubleshooting

**Bot doesn't reply to anything.**
- *Settings → Telegram Bot* shows a connection-status block. If polling
  hasn't started, the token is probably wrong. Re-paste it.
- The bot only replies to the paired chat. If you're texting from a chat
  that wasn't paired, the bot ignores you. Generate a new pairing code and
  send `/start <code>` from the right chat.

**Bot replies "This bot is connected to someone else's Pennywise."**
- A different chat sent a message after pairing. Pennywise is single-tenant
  per install — only one chat can talk to the bot.
- *Settings → Telegram Bot → Disconnect* clears the chat ID; you can pair a
  new chat after that.

**Bot replies "Image uploads need a vision-capable model."**
- All four catalog models in Pennywise support vision. If you see this, the
  saved model ID may not match the current catalog — re-save in
  *Settings → LLM Provider*.

**Bot replies "Your provider (xai) doesn't accept image/webp."**
- xAI accepts only JPG and PNG. Re-export the receipt or switch providers.

**Receipt photos arrive but parsing produces wrong totals.**
- Try a clearer photo with the total clearly visible.
- For partial captures, include a caption with the missing detail.
- Edit the parsed expense via the **✏️ Edit** button on the confirmation.

## Stopping or removing the bot

- **Disable polling:** *Settings → Telegram Bot → Disable*. The bot stops
  receiving messages but its config is retained.
- **Disconnect the chat:** *Settings → Telegram Bot → Disconnect*. Pairing
  state cleared; bot still configured.
- **Remove entirely:** *Settings → Telegram Bot → Remove bot*. Token,
  username, chat ID all wiped. Recreate by pasting a new token.

The bot itself in Telegram is unaffected by any of these — it remains
discoverable, just disconnected from your Pennywise install.
