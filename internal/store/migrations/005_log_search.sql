-- Full-text search over review titles and bodies.
CREATE VIRTUAL TABLE logs_fts USING fts5(name, review, content='logs', content_rowid='id');

CREATE TRIGGER logs_ai AFTER INSERT ON logs BEGIN
  INSERT INTO logs_fts(rowid, name, review) VALUES (new.id, new.name, new.review);
END;

CREATE TRIGGER logs_ad AFTER DELETE ON logs BEGIN
  INSERT INTO logs_fts(logs_fts, rowid, name, review) VALUES ('delete', old.id, old.name, old.review);
END;

CREATE TRIGGER logs_au AFTER UPDATE OF name, review ON logs BEGIN
  INSERT INTO logs_fts(logs_fts, rowid, name, review) VALUES ('delete', old.id, old.name, old.review);
  INSERT INTO logs_fts(rowid, name, review) VALUES (new.id, new.name, new.review);
END;

INSERT INTO logs_fts(logs_fts) VALUES ('rebuild');
