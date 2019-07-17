package main

//Предзаполненная база для тестов:
//Пользователи:
//	admin — имеет свои записи, делится с другими, авторизован (есть сессия)
//	user — имеет свои записи, делится с другими, имеет приватную запись, авторизован
//	guest — не имеет своих записей, но имеет расшаренные записи, авторизован
//	ghost — не имеет своих записей, не имеет расшаренных записей, не авторизован
//Аудиозаписи:
//	первая тесовая запись (test music) должна ссылаться на существующий файл
//	sample.ogg в каталоге ./media. Контрольная сумма по нему посчитана и внесена
//	в константу audiofill_test->crcSample для теста скачивания файла Audio.Get
//	Все остальные записи в БД фиктивные (можно проверять ошибку доступа к несуществ. файлу)

var pgDump = `
DROP TABLE IF EXISTS share CASCADE;
DROP TABLE IF EXISTS audio CASCADE;
DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP SEQUENCE IF EXISTS user_id_seq;
DROP SEQUENCE IF EXISTS audio_id_seq;

CREATE SEQUENCE user_id_seq;
CREATE SEQUENCE audio_id_seq;

CREATE TABLE users (
    id_user integer DEFAULT nextval('user_id_seq'::regclass) NOT NULL PRIMARY KEY,
    login character varying(255) NOT NULL UNIQUE,
    name character varying(255) NOT NULL default '',
    password character varying(48) NOT NULL
);

CREATE TABLE sessions (
	id_user integer not null UNIQUE REFERENCES users(id_user),
	id_session varchar(32) not null
);

CREATE TABLE audio (
    id_audio integer DEFAULT nextval('audio_id_seq'::regclass) NOT NULL PRIMARY KEY,
    description character varying DEFAULT '' NOT NULL,
    duration interval(0) DEFAULT '00:00:00'::interval NOT NULL,
	id_owner integer NOT NULL REFERENCES users(id_user),
	filename varchar not null default ''
);
CREATE INDEX audio_by_name ON audio (description);	-- for fast ORDER BY name|user
CREATE INDEX audio_by_owner ON audio (id_owner);

CREATE TABLE share (
	id_audio integer not null REFERENCES audio(id_audio),
	id_user  integer not null REFERENCES users(id_user)
);
CREATE INDEX ON share (id_audio);	-- for JOIN audio ON (id_audio)
CREATE INDEX ON share (id_user);	-- for search shared tracks by id_user

INSERT INTO users
VALUES  (default, 'admin', '', 'ea847988ba59727dbf4e34ee75726dc3'),
		(default, 'user', 'Lorem Ipsum', '5ebe2294ecd0e0f08eab7690d2a6ee69'),
		(default, 'guest', 'Uninvited T', 'a32c3d3cec20f5a09595b857e45b477f'),
		(default, 'ghost', 'Dutchman Flying', 'e10adc3949ba59abbe56e057f20f883e');

INSERT INTO sessions
VALUES  (1, '3d73274ac8b18ab09528075c7fee1213'),
		(2, 'b00f30ecdfa4d5bd2e5280ab59be492a'),
		(3, '0414d6d5d923b0f4998556df2fe2e351');

INSERT INTO audio 
VALUES  (default, 'test music', '00:04:00', 1, 'sample.ogg'),
		(default, 'best music', '00:14:00', 1, 'rock.ogg'),
		(default, 'bad music', '00:01:00', 2, 'pop.ogg'),
		(default, 'private music', '00:10:00', 2, 'never_to_share.ogr');

INSERT INTO share VALUES (1,2),(1,3),(2,2),(3,1),(3,3);
`
