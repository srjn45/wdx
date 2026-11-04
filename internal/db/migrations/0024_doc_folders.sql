-- The folder tide shows a doc or plan in (docs/FOLDERS.md): only a label, which
-- nothing looks a row up by, so no index. Older binaries leave it alone, and what
-- they create is at the top.
ALTER TABLE docs ADD COLUMN folder TEXT NOT NULL DEFAULT '/';
