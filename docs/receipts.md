# Receipt uploads

Pennywise can extract an expense from a photo of a receipt — both in the
dashboard (drop a file on `/expenses/new`) and in the Telegram bot (send a
photo to your bot).

The dashboard pre-fills the new-expense form so you can review before saving.
The Telegram path uses the same confirmation flow as text messages, including
the [low-confidence prompt](telegram.md#low-confidence-prompts) when the
parse is ambiguous.

## What's accepted

| Provider     | Accepted formats              | Max file size |
|--------------|-------------------------------|---------------|
| OpenAI       | JPG, PNG, WebP, GIF           | 10 MB         |
| Anthropic    | JPG, PNG, WebP, GIF           | 10 MB         |
| Google       | JPG, PNG, WebP, HEIC, PDF     | 10 MB         |
| xAI (Grok)   | JPG, PNG only                 | 10 MB         |

Pennywise's outer envelope is JPG / PNG / WebP / HEIC / PDF / 10 MB. Each
provider may narrow that further; if your image format isn't accepted by the
configured provider you'll see a clear inline message naming the formats that
are.

The 10 MB cap covers everyday phone photos with headroom; raise it only if
you need to and you're aware of the cost (vision LLM calls are 5–10× more
expensive than text calls and are billed per token, with image tokens
proportional to resolution).

## What you get back

Tier 0 — **one image, one rolled-up expense**:

- Description: a short merchant-style label like *"Groceries at Walmart"*
  or *"Dinner at Café"*. We don't itemize; the prompt explicitly tells the
  model to roll up to a single line.
- Amount: the receipt total in your home currency.
- Date: the receipt's printed date if visible, today otherwise.
- Category: best fuzzy match against your existing categories, falling back
  to *Other*.
- Ledger: matched against your active ledgers when the receipt mentions a
  hint, otherwise none.

If the model thinks the image isn't a receipt (a screenshot, a random photo,
etc.), you get a one-line "this isn't a receipt" message instead — nothing
is logged.

## Privacy notes

Image bytes are sent to the configured LLM provider over HTTPS for parsing.
Pennywise doesn't keep the file: after the parse returns we discard it. The
parsed expense is stored normally; the original image is not.

Receipts often contain partial card numbers, addresses, and other PII.
Treat the LLM provider as you'd treat any other third-party processor and
review their data-handling policies if your jurisdiction cares.

## Why no attachments are stored

We considered persisting receipts so you could view them later in the
expense detail page. We deliberately punted — for v2 the costs (storage,
soft-delete cascade, MIME revalidation, an attachments table) outweighed the
benefit, and Telegram already keeps your photo in the chat history. If
demand for stored receipts grows we'll revisit.

## Where to get help

- "Drop zone doesn't appear on `/expenses/new`": only shown in *new* mode,
  not edit. Hard-refresh the page if you just upgraded.
- "Bot says photos aren't supported": that string is the pre-v2.4 stub.
  Restart the binary after upgrading.
- Other failure modes are documented per-surface in
  [`llm-providers.md`](llm-providers.md) and [`telegram.md`](telegram.md).
