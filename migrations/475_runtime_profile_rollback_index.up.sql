-- Reserve the reverse-order step that restores legacy uniqueness before
-- dropping any personal index. The existing legacy index stays until 476.up.
SELECT 1;
