-- A way back in for someone who forgot their password, without email.
--
-- Nothing here sends mail, and a church volunteer who forgets a password was
-- locked out for good. An administrator of their church can hand them a short
-- code instead, in person or by message; the code and the email together set
-- a new password.
--
-- One live code per account: making a new one replaces the old. Only a hash is
-- stored, the code expires, and a few wrong guesses throw it away.
create table if not exists password_reset_codes (
    user_id    uuid        primary key references users (id) on delete cascade,
    code_hash  bytea       not null,
    created_by uuid        references users (id) on delete set null,
    expires_at timestamptz not null,
    attempts   int         not null default 0
);
