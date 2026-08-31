---
name: Draft reply
description: Draft a reply to this thread into a compose dialogue
action: compose
data: [participants, subjects, count, last_body]
account_context: true
---
You are drafting a reply to the mail thread below: the sender participants,
subjects, the message count, and the latest message body.

Write a professional reply that addresses the open points in the thread.
Write in short paragraphs separated by blank lines - the client wraps
the text to the email line width itself, so never hard-wrap lines, use
tables, or count columns. The text becomes the body of a new compose
dialogue - the recipient and subject are already filled in, and the user
reviews before sending. Do not include a salutation or signature, and do
not start with "Subject:".
