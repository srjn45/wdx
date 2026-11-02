-- A terminal row with an editor_path runs the editor named editor on that file,
-- relative to its dir, and is deleted when the editor ends. Its kind stays 'shell':
-- adding a kind to the CHECK would rebuild terminals, whose DROP would cascade into
-- agents and messages. So an older binary sees a shell.
ALTER TABLE terminals ADD COLUMN editor TEXT;
ALTER TABLE terminals ADD COLUMN editor_path TEXT;
