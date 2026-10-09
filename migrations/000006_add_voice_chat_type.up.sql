-- Добавляем тип чата "voice" (голосовая комната) в существующий ENUM.
-- Значения ENUM расширяются без пересоздания таблицы; операция идемпотентна.
ALTER TYPE chat_type ADD VALUE IF NOT EXISTS 'voice';
