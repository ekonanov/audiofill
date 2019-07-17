package main

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	_ "github.com/lib/pq"
)

//newSession create new session for user uid
//	Продолжительность сессии системой никак не контролируется. Пользователь
//	может закрыть браузер в любой момент и оборвать сессию без уведомления
//	сервера. При следующем входе создастся новая сессия с другим ИД.
//	Чтобы в БД обеспечить уникальность сессии (для одного id_user — одна
//	id_session), в случае конфликта делаем обновление старой записи
func newSession(db *sql.DB, uid int) (sessID string, err error) {
	b := make([]byte, 16)
	_, err = rand.Read(b)
	if err != nil {
		return
	}
	sessID = fmt.Sprintf("%x", b)
	_, err = db.Exec(`INSERT INTO sessions (id_user, id_session) VALUES ($1, $2)
		ON CONFLICT (id_user) DO UPDATE set id_session = $2 
		WHERE sessions.id_user = $1`, uid, sessID)
	return
}

//checkSession	проверяет наличие активной сессии пользователя по куке session_id
//  при наличии сесии возвращает соответсвующий userID
func checkSession(db *sql.DB, r *http.Request) (userID int, err error) {
	var sessID *http.Cookie

	sessID, err = r.Cookie("session_id")
	if err != nil {
		return
	}

	err = db.QueryRow("Select id_user From sessions Where id_session = $1", sessID.Value).Scan(&userID)
	return
}

//getPageno получает из параметров запроса номер страницы и кол-во строк на странице
func getPageno(r *http.Request) (pg, ln int) {
	var (
		err    error
		strVal []string
		isSet  bool
	)

	strVal, isSet = r.Form["page_no"]
	if isSet {
		pg, err = strconv.Atoi(strVal[0])
		if err != nil {
			pg = 0
		} else if pg <= 0 { // отрицательные значения игнорируем/исправляем
			pg = 0
		} else { // "человеческая" нумерация начинается с 1, машинная — с 0. Приводим номер к машинному
			pg--
		}
	} else {
		pg = 0
	}

	strVal, isSet = r.Form["on_page"]
	if isSet {
		ln, err = strconv.Atoi(strVal[0])
		if err != nil {
			ln = 10
		} else if ln <= 0 { // отрицательные значения игнорируем/исправляем
			ln = 10
		}
	} else {
		ln = 10
	}
	return
}
