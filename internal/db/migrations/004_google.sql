CREATE TABLE IF NOT EXISTS user_providers (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  provider TEXT NOT NULL CHECK (provider='google'),
  subject TEXT NOT NULL,
  email TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, subject),
  UNIQUE (provider, user_id)
);

CREATE TABLE IF NOT EXISTS google_calendar_tokens (
  user_id BIGINT PRIMARY KEY,
  refresh_token TEXT NOT NULL,
  scope TEXT NOT NULL DEFAULT 'calendar.events',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
