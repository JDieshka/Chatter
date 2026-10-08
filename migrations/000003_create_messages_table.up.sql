CREATE TABLE messages (
    id         BIGSERIAL PRIMARY KEY,
    chat_id    UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    sender_id  UUID NOT NULL REFERENCES users(id),
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_messages_chat_created ON messages(chat_id, created_at DESC);
