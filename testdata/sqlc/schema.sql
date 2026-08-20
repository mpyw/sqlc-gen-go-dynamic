create table users (
    id      bigint not null,
    name    text not null,
    status  text not null,
    age     int not null,
    balance numeric not null,
    seen_at timestamptz,
    tags    text[],
    note    text
);
