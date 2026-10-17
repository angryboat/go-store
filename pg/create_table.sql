CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
  "version" TEXT PRIMARY KEY CHECK (char_length("version") > 0),
  "checksum" BYTEA NOT NULL CHECK (octet_length("checksum") = 32),
  "applied_at" TIMESTAMPTZ NOT NULL DEFAULT CLOCK_TIMESTAMP()
);
