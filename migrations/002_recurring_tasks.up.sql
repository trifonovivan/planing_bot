ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS recurrence_rule TEXT;
