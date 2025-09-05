CREATE TABLE IF NOT EXISTS plans (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  topic TEXT NOT NULL,
  level TEXT NOT NULL DEFAULT 'beginner',
  hours_per_week INT NOT NULL DEFAULT 4,
  start_date DATE NOT NULL,
  weeks INT NOT NULL DEFAULT 4,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plan_tasks (
  id BIGSERIAL PRIMARY KEY,
  plan_id BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  description TEXT,
  start_time TIMESTAMPTZ NOT NULL,
  end_time   TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','completed')),
  order_no INT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_plan_tasks_plan_time ON plan_tasks(plan_id, start_time);
