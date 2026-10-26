-- Run as the schema owner with psql -v application_role=<existing API role>.
-- Do not grant to PUBLIC or change the application's other privileges.
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE personal_secret TO :"application_role";
