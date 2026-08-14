# Account selection for sending + async send failure - design draft

DRAFT - design input captured 2026-08-14, not yet brainstormed or
approved. Milestone: R4 (async send + dialogue state machine) with the
R2 account data model. Normative text is AGENTS.md; this file records
requirements so they are not lost.

## 1. Problem

mutt's account selection for sending is send-hook based: regex hooks
matching on the mailbox/folder context pick a `From:`. For a tag-based
notmuch client that is brittle: there is no folder context in a tag
view, a message can carry several account-relevant tags, and the
hook chain grows with every account. The client needs account selection
that is data, not hook code.

## 2. Requirements (from the user, verbatim intent)

### Account selection

- Easy to switch accounts while composing.
- Default selection: the account that matches the received message -
  replying to a message received on account X sends from account X,
  without configuration per folder.
- Gmail allows arbitrary dots in the local part: user@gmail.com,
  u.ser@gmail.com, us.er@gmail.com are all the same account.
- Gmail plus addressing: +<any string>@gmail.com is the same account.
- Custom-domain accounts may have wildcard senders: anysender@mydomain.com
  is a valid sender when the domain belongs to the account, and msmtp
  supports that sending case (no verification that the local part
  exists).

### Async send

- Send runs in the background (msmtp), but the client still waits for a
  completion notification - the user must not be left wondering.
- If the send fails, reopen the background/send dialog WITHOUT being
  destructive to the current action: the dialogue state (draft,
  attachments, flags) survives; failure reopens it for retry/edit.
  Launching the reopened dialogue as a background tab is one of the
  acceptable shapes (R4 dialogue state machine territory: state is
  separate from UI, dialogues can be paused and restarted).

## 3. Model sketch (for the design session, not settled)

- Accounts are already data in R2: `[accounts.<name>] folder = "gmail"`,
  account tags derived from the folder prefix (folder:/^gmail\//). The
  received message's account comes from the same data - the message's
  account tag or folder-derived account, no new per-folder config.
- Per account, a From-address table with NORMALIZED matching:
  - exact address
  - gmail normalization: strip dots from the local part, strip +suffix,
    compare canonical form
  - wildcard domain: `*@mydomain.com` matches any local part (msmtp
    accepts it; the domain is the account's, trust boundary is the
    domain)
- Normalization is matching logic, not a prompt and not shell
  interpolation: the selected From goes into the MIME header assembly
  (R4) and the msmtp argv (F4 - argv exec only, never a shell string).
- Account switch during compose is a dialogue-state action (R4), not a
  config mutation.

## 4. Open questions for the design session

- Does the received-account match use the account tag, the folder
  prefix, or both as fallback?
- What does "easy to switch" look like in the compose UI - a selector
  in the compose header, a key binding, both?
- Failure reopen: does the failed send job's captured output feed the
  reopened dialog (the neomutt bg_dialog pattern: output kept for
  review)?
- Per-account msmtp invocation: one config file with account sections
  (msmtp -a <name>), or per-account argv? Which maps to the R10
  Provider boundary?

## 5. Non-goals for this draft

No SMTP client implementation (transport stays external tools per the
non-goals). This milestone is the send JOB + dialogue state machine
(R4), not the transport.
