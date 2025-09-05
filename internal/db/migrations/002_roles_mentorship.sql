CREATE TABLE IF NOT EXISTS user_roles (
  user_id BIGINT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('student','mentor')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, role)
);

CREATE TABLE IF NOT EXISTS mentorship_requests (
  id BIGSERIAL PRIMARY KEY,
  student_id BIGINT NOT NULL,
  mentor_id BIGINT NOT NULL,
  message TEXT,
  status TEXT NOT NULL CHECK (status IN ('pending','approved','declined','cancelled')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  decided_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_pending_request
  ON mentorship_requests(student_id, mentor_id)
  WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS mentorships (
  id BIGSERIAL PRIMARY KEY,
  student_id BIGINT NOT NULL,
  mentor_id BIGINT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('active','paused','ended')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_active_mentorship
  ON mentorships(student_id, mentor_id)
  WHERE status = 'active';
