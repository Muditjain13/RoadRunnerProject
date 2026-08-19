-- MYSQL_DATABASE env var only creates the first database (inventory).
-- booking and payment each own their tables; create their databases here.
CREATE DATABASE IF NOT EXISTS booking;
CREATE DATABASE IF NOT EXISTS payment;
