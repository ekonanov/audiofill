package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

type tUser struct {
	UserID int    `json:"id"`
	Name   string `json:"name"`
	Login  string `json:"login,omitempty"`
	Shared int    `json:"shared_records,omitempty"`
}

type tUsrList struct {
	Count int      `json:"total_count,omitempty"`
	List  []*tUser `json:"users"`
}

//Users класс для обслуживания запросов к таблице "users":
//	добавление нового (регистрация), проверка логина/пароля, список
type Users struct {
	DB     *sql.DB `json:"-"`
	userID int
}

//NewUsers создание нового экземпляра класса Users
func NewUsers(db *sql.DB) *Users {
	return &Users{
		DB: db,
	}
}

//Registration регистрация нового пользователя в системе. Метод PUT
//Параметры: login, passwd обязательные, name. Login должен быть уникальным
//Результат: статус "Created", назначенный id новому пользователю {"id":<ddd>, }
//Ошибка: статус "MethodNotAllowed" если метод не PUT
//	статус "BadRequest" если отсутствуют обязательные параметры или логин занят,
//	статус "InternalServerError" в остальных случаях
func (usr *Users) Registration(resp http.ResponseWriter, req *http.Request) {
	var (
		sqlQuery string        //	текст запроса
		sqlParam []interface{} //	параметры запроса
		qr       *sql.Row      //	результаты запроса
		err      error
		isSet    bool
		frmVal   []string
		userID   int
	)

	resp.Header().Set("Content-Type", "text/plain")
	if req.Method != http.MethodPut {
		http.Error(resp, "bad method", http.StatusMethodNotAllowed)
		return
	}

	err = req.ParseForm()
	if err != nil {
		http.Error(resp, "wrong form data", http.StatusBadRequest)
		return
	}

	sqlQuery = `INSERT INTO users (login, password, name) 
		VALUES ($1, md5($2),`

	frmVal, isSet = req.Form["login"]
	if !isSet {
		http.Error(resp, "login required", http.StatusBadRequest)
		return
	}
	sqlParam = append(sqlParam, frmVal[0])

	frmVal, isSet = req.Form["passwd"]
	if !isSet {
		http.Error(resp, "password required", http.StatusBadRequest)
		return
	}
	sqlParam = append(sqlParam, frmVal[0])

	frmVal, isSet = req.Form["name"]
	if isSet {
		sqlParam = append(sqlParam, frmVal[0])
		sqlQuery += fmt.Sprintf("$%d,", len(sqlParam))
	} else {
		sqlQuery += "default,"
	}

	sqlQuery = strings.TrimRight(sqlQuery, ",") + ") RETURNING id_user"

	qr = usr.DB.QueryRow(sqlQuery, sqlParam...)
	err = qr.Scan(&userID)
	if err != nil {
		if pgErr, ok := err.(*pq.Error); ok {
			switch pgErr.Code {
			case "23505": // unique constraint violation
				http.Error(resp, "login already used", http.StatusBadRequest)
				log.Println("Users.Registration query failed:", pgErr.Message, pgErr.Detail)
			default:
				http.Error(resp, pgErr.Message, http.StatusBadRequest)
				log.Println("Users.Registration query failed:", pgErr.Message, pgErr.Detail)
			}

		} else {
			http.Error(resp, "internal error", http.StatusInternalServerError)
			log.Println("Users.Registration query failed:", err.Error())
		}
		return
	}

	resp.WriteHeader(http.StatusCreated)
}

//Login вход в систему, проверка правильности login/passwd по базе зарегистрированных
//	пользователей. Метод POST. Параметры login, passwd — обязательны
//Результат: новая сессия пользователя, установлен куки {"session_id": <xxx>}
//Ошибка: статус "NotFound" если логин/пароль не совпадают с зарегистрированными
//	статус "MethodNotAllowed" если метод не равен POST
//	статус "BadRequest" если отсутствуют обязательные параметры
//	статус "InternalServerError" в остальных случаях
func (usr *Users) Login(resp http.ResponseWriter, req *http.Request) {
	var (
		qr       *sql.Row
		err      error
		sqlParam []interface{}
		frmVal   []string
		isSet    bool
		userID   int
	)

	resp.Header().Set("Content-Type", "text/plain")
	if req.Method != http.MethodPost {
		http.Error(resp, "bad method", http.StatusMethodNotAllowed)
		return
	}

	err = req.ParseForm()
	if err != nil {
		http.Error(resp, "wrong form data", http.StatusBadRequest)
		return
	}

	frmVal, isSet = req.Form["login"]
	if !isSet {
		http.Error(resp, "login required", http.StatusBadRequest)
		return
	}
	sqlParam = append(sqlParam, frmVal[0])

	frmVal, isSet = req.Form["passwd"]
	if !isSet {
		http.Error(resp, "password required", http.StatusBadRequest)
		return
	}
	sqlParam = append(sqlParam, frmVal[0])

	qr = usr.DB.QueryRow(`SELECT id_user 
		FROM users 
		WHERE login = $1 and password = md5($2)`, sqlParam...)
	err = qr.Scan(&userID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(resp, "wrong login or password", http.StatusNotFound)
		} else {
			http.Error(resp, "internal error", http.StatusInternalServerError)
			log.Println("Users.Login query failed:", err.Error())
		}
		return
	}

	sessID, err := newSession(usr.DB, userID)
	if err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		log.Println("Users.Login make session failed:", err.Error())
		return
	}

	sessCook := &http.Cookie{Name: "session_id", Value: sessID}
	http.SetCookie(resp, sessCook)
	resp.WriteHeader(http.StatusOK)
}

//Logout закрывает сессию пользователя, удаляет соотв. запись из sessions
//Результат: статус "Ок", возвращает куки "session_id" с истекшей датой
//	(хак для удаления куки из браузера пользователя)
//Ошибка: -
func (usr *Users) Logout(resp http.ResponseWriter, req *http.Request) {
	var (
		err      error
		sessCook *http.Cookie
	)

	usr.userID, err = checkSession(usr.DB, req)
	if err == http.ErrNoCookie {
		//	сессии и так нет, все хорошо
		resp.WriteHeader(http.StatusOK)
		return
	}
	sessCook = &http.Cookie{Name: "session_id", Value: "", Expires: time.Now().Add(-10 * time.Minute)}
	http.SetCookie(resp, sessCook)

	if err != nil { //	сессия была, но userID прочитать не удалось?
		http.Error(resp, "internal error", http.StatusInternalServerError)
		return
	}

	//	есть сессия, есть userID: правим в базе
	_, err = usr.DB.Exec("DELETE FROM sessions WHERE id_user = $1", usr.userID)
	if err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		return
	}
	resp.WriteHeader(http.StatusOK)
}

//List список зарегистрированных в системе пользователей. Метод GET,
//	доступен для авторизованных пользователей
//Параметры: page_no — номер страницы, on_page — кол-во записей на странице
//	необязательные, по умолчанию 1 и 10 соответственно
//Результат: статус ОК, json список пользователей
//Ошибка: статус MethodNotAllowed если метод не равен GET
//	статус Unauthorized если пользователь не авторизован
//	статус BadRequest, InternalServerError при прочих ошибках
func (usr *Users) List(resp http.ResponseWriter, req *http.Request) {
	var (
		pg, ln int // номер страницы и кол-во строк ("длина" страницы)
		qr     *sql.Rows
		err    error
		u      *tUser
		uLst   tUsrList
		jsRes  []byte
	)

	if req.Method != http.MethodGet {
		http.Error(resp, "bad method", http.StatusMethodNotAllowed)
		return
	}

	usr.userID, err = checkSession(usr.DB, req)
	if err != nil {
		http.Error(resp, "access denied", http.StatusUnauthorized)
		return
	}

	err = req.ParseForm()
	if err != nil {
		http.Error(resp, "wrong form data", http.StatusBadRequest)
		return
	}
	pg, ln = getPageno(req)

	qr, err = usr.DB.Query(`SELECT id_user, login, name 
		FROM users ORDER BY id_user
		OFFSET $1 LIMIT $2`, pg*ln, ln)
	if err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		if pgErr, ok := err.(*pq.Error); ok {
			log.Println("Users.List query failed:", pgErr.Message, pgErr.Detail)
		} else {
			log.Println("Users.List query failed:", err.Error())
		}
		return
	}
	defer qr.Close()

	uLst = tUsrList{}
	//	sql.Rows в случае пустого списка не генерит ошибку ErrNoRows, проверяем сами
	if qr.Next() {
		for {
			u = &tUser{}
			err = qr.Scan(&u.UserID, &u.Login, &u.Name)
			if err != nil {
				log.Println("Users.List query iteration error:", err.Error())
				continue
			}
			uLst.List = append(uLst.List, u)
			if !qr.Next() {
				break
			}
		}
	} else {
		http.Error(resp, "", http.StatusNotFound)
		return
	}
	if qr.Err() != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		log.Println("Users.List query iteration error:", err.Error())
		return
	}

	jsRes, err = json.Marshal(uLst)
	if err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		log.Println("Users.List result marshaling error:", err.Error())
		return
	}

	resp.WriteHeader(http.StatusOK)
	resp.Write(jsRes)
}

//Share список пользователей, имеющих "расшаренные" треки с количеством таких треков
//	Метод GET, доступен только для авторизованных пользователей
//Параметры: page_no — номер страницы, on_page — кол-во записей на странице
//	необязательные, по умолчанию 1 и 10 соответственно
//Результат: статус ОК, json список пользователей
//Ошибка: статус MethodNotAllowed если метод не равен GET
//	статус Unauthorized если пользователь не авторизован
//	статус BadRequest, InternalServerError при прочих ошибках
func (usr *Users) Share(resp http.ResponseWriter, req *http.Request) {
	var (
		err    error
		pg, ln int
		qr     *sql.Row
		qs     *sql.Rows
		u      *tUser
		uLst   tUsrList
	)
	if req.Method != http.MethodGet {
		http.Error(resp, "bad method", http.StatusMethodNotAllowed)
		return
	}

	usr.userID, err = checkSession(usr.DB, req)
	if err != nil {
		http.Error(resp, "access denied", http.StatusUnauthorized)
		return
	}

	err = req.ParseForm()
	if err != nil {
		http.Error(resp, "wrong form data", http.StatusBadRequest)
		return
	}
	pg, ln = getPageno(req)

	uLst = tUsrList{}
	//	список и общее количество расшаривших пользователей можно бы подсчитать
	//	в одном запросе (group by rollup…), но offset…limit отрезает итоговую
	//	строку из результата. Никак ее выделить отдельно не получилось — поэтому
	//	получаем данные в два запроса:
	//	(т.к.: cannot insert multiple commands into a prepared statement)
	qr = usr.DB.QueryRow(`-- общее количество пользователей, расшаривших треки
		SELECT count(distinct id_owner)
		FROM audio
		WHERE id_audio in (SELECT distinct id_audio FROM share)`)

	err = qr.Scan(&uLst.Count)
	if err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		if pgErr, ok := err.(*pq.Error); ok {
			log.Println("Users.Share scan count failed:", pgErr.Message, pgErr.Detail)
		} else {
			log.Println("Users.Share scan count failed:", err.Error())
		}
		return
	}

	qs, err = usr.DB.Query(`-- список пользователей
		SELECT a.id_owner, coalesce(nullif(u.name,''),u.login) as name, count(id_audio)
		FROM audio a
		INNER JOIN users u on (a.id_owner  = u.id_user)
		WHERE exists(SELECT id_audio from share s where s.id_audio = a.id_audio)
		GROUP BY id_owner, coalesce(nullif(u.name,''),u.login)
		ORDER BY id_owner
		OFFSET $1 LIMIT $2`, pg*ln, ln)

	if err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		if pgErr, ok := err.(*pq.Error); ok {
			log.Println("Users.Share query list failed:", pgErr.Message, pgErr.Detail)
		} else {
			log.Println("Users.Share query list failed:", err.Error())
		}
		return
	}
	defer qs.Close()

	//	sql.Rows в случае пустого списка не генерит ошибку ErrNoRows, проверяем сами
	if !qs.Next() {
		http.Error(resp, "", http.StatusNotFound)
		return
	}
	for {
		u = &tUser{}
		err = qs.Scan(&u.UserID, &u.Name, &u.Shared)
		if err != nil {
			log.Println("Users.Share scan list error:", err.Error())
			continue
		}
		uLst.List = append(uLst.List, u)
		if !qs.Next() {
			break
		}
	}
	if qs.Err() != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		log.Println("Users.Share query iteration error:", err.Error())
		return
	}

	jsRes, err := json.Marshal(uLst)
	if err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		log.Println("Users.Share result marshaling error:", err.Error())
		return
	}

	resp.WriteHeader(http.StatusOK)
	resp.Write(jsRes)
}
