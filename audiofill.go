package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

const mediaDir = "media"

type tShare struct {
	AudioID  int    `json:"audio,omitempty"`
	UserID   int    `json:"id"`
	UserName string `json:"name"`
}

type tAudio struct {
	AudioID   int    `json:"id"`
	Descr     string `json:"name"`
	IsOwn     bool   `json:"is_owner"`
	OwnerID   int    `json:"owner_id"`
	OwnerName string `json:"owner_name"`

	Shared []*tShare `json:"shared_to"`
}

type tAudioList struct {
	Count int       `json:"total_count"`
	List  []*tAudio `json:"records"`
}

//Audiofill класс для таблиц audio/share
//	добавление/удаление аудиозаписей, просмотр списка записей, "расшаривание"
//	получение (скачивание) файла аудиозаписи
type Audiofill struct {
	DB     *sql.DB
	userID int
}

//NewAudiofill создание нового экземпляра класса Audiofill
func NewAudiofill(db *sql.DB) *Audiofill {
	return &Audiofill{
		DB: db,
	}
}

//List cписок доступных пользователю аудиозаписей. Метод GET, доступен только для авторизованных
//Параметры: page_no номер страницы, on_page строк на странице, необязательные
//	по умолчанию 1 и 10 соответственно.
//	order_by поле сортировки, допустимые значения user|track, default — user
//Результат:
//Ошибка:
func (afl *Audiofill) List(resp http.ResponseWriter, req *http.Request) {
	var (
		err      error
		pg, ln   int
		ord      string
		sqlQuery string

		qr      *sql.Row
		qs      *sql.Rows
		aLst    tAudioList
		curAd   *tAudio
		sqlID   sql.NullInt64
		sqlName sql.NullString
		jsRes   []byte
	)

	orderBy := map[string]string{
		"user":  "3 desc, 4, 2",
		"track": "2",
	}
	if req.Method != http.MethodGet {
		http.Error(resp, "bad method", http.StatusMethodNotAllowed)
		return
	}

	afl.userID, err = checkSession(afl.DB, req)
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

	if strVal, ok := req.Form["order_by"]; ok {
		if ord, ok = orderBy[strVal[0]]; !ok {
			http.Error(resp, "bad parameter order_by", http.StatusBadRequest)
			return
		}
	} else {
		ord = orderBy["user"]
	}

	aLst = tAudioList{}
	//	т.к.: cannot insert multiple commands into a prepared statement
	//	получаем данные в два запроса:
	qr = afl.DB.QueryRow(`--общее количество доступных пользователю записей
		SELECT count(distinct id_audio)
		FROM audio
		WHERE id_owner = $1	-- собственные
			OR id_audio in ( -- расшаренные другими
				SELECT id_audio FROM share WHERE id_user = $1
			)
		`, afl.userID)
	err = qr.Scan(&aLst.Count)
	if err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		//	если это ошибка постгреса, в логе можем сохранить более детальную информацию
		if pgErr, ok := err.(*pq.Error); ok {
			log.Println("Audio.List scan count failed:", pgErr.Message, pgErr.Detail)
		} else {
			log.Println("Audio.List scan count failed:", err.Error())
		}
		return
	}

	sqlQuery = fmt.Sprintf(`-- список записей с постраничной разбивкой
		WITH available AS (
			SELECT  a.id_audio, 
				concat(a.description,' (',a.duration,')') as name, 
				a.id_owner = $1 as is_owner,
				a.id_owner,
				coalesce(nullif(own.name,''), own.login) as owner_name
				
			FROM audio a
			INNER JOIN users own on (a.id_owner = own.id_user)

			WHERE a.id_owner = $1	-- собственные
				OR a.id_audio in ( -- расшаренные другими
						SELECT id_audio FROM share WHERE id_user = $1
				)
			ORDER BY %s
			OFFSET $2 LIMIT $3
			)
		SELECT av.*, usr.id_user,
			coalesce(nullif(usr.name, ''), usr.login) as user_name
		FROM available av
		LEFT JOIN share sh ON (sh.id_audio = av.id_audio)
		LEFT JOIN users usr ON (sh.id_user = usr.id_user)
		ORDER BY %s, 6
		`, ord, ord)

	qs, err = afl.DB.Query(sqlQuery, afl.userID, pg*ln, ln)
	if err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		if pgErr, ok := err.(*pq.Error); ok {
			log.Println("Audio.List query list failed:", pgErr.Message, pgErr.Detail)
		} else {
			log.Println("Audio.List query list failed:", err.Error())
		}
		return
	}
	defer qs.Close()

	//	sql.Rows в случае пустого списка не генерит ошибку ErrNoRows, проверяем сами
	if !qs.Next() {
		http.Error(resp, "", http.StatusNotFound)
		return
	}

	curAd = &tAudio{}
	err = qs.Scan(&curAd.AudioID, &curAd.Descr, &curAd.IsOwn, &curAd.OwnerID, &curAd.OwnerName, &sqlID, &sqlName)
	if err != nil {
		http.Error(resp, "", http.StatusInternalServerError)
		log.Println("Audio.List query scan error:", err.Error())
		return
	}
	if sqlID.Valid { //	null-значения не добавляем
		afl.appendShare(curAd, sqlID, sqlName)
	}

	for qs.Next() {
		ad := &tAudio{}
		err = qs.Scan(&ad.AudioID, &ad.Descr, &ad.IsOwn, &ad.OwnerID, &ad.OwnerName, &sqlID, &sqlName)
		if err != nil {
			http.Error(resp, "", http.StatusInternalServerError)
			log.Println("Audio.List query scan error:", err.Error())
			return
		}

		if ad.AudioID == curAd.AudioID { //	добавляем список "расшаренных" в текущую запись
			afl.appendShare(curAd, sqlID, sqlName)

		} else { //	новая запись ­— сохраним "старую" и создадим новую
			aLst.List = append(aLst.List, curAd)

			afl.appendShare(ad, sqlID, sqlName)
			curAd = &tAudio{}
			afl.copyAudio(curAd, ad)
		}
	}
	aLst.List = append(aLst.List, curAd)

	jsRes, err = json.Marshal(aLst)
	if err != nil {
		http.Error(resp, "Audio.List result marshaling error", http.StatusInternalServerError)
		log.Println("Audio.List result marshaling error", err.Error())
		return
	}
	resp.WriteHeader(http.StatusOK)
	resp.Write(jsRes)
}

//Share “Расшарить” аудиозапись. Метод POST, доступен только авторизованным пользователям
//Параметры: track — id аудиозаписи, к которой предоставляется доступ
//	user — пользователь, которому предоставляется доступ
//Результат: статус ОК
//Ошибка:
func (afl *Audiofill) Share(resp http.ResponseWriter, req *http.Request) {
	var (
		err error
		ok  bool

		frmVal  []string
		tr, usr int
	)
	if req.Method != http.MethodPost {
		http.Error(resp, "bad method", http.StatusMethodNotAllowed)
		return
	}

	if afl.userID, err = checkSession(afl.DB, req); err != nil {
		http.Error(resp, "access denied", http.StatusUnauthorized)
		return
	}

	err = req.ParseForm()
	if err != nil {
		http.Error(resp, "wrong form data", http.StatusBadRequest)
		return
	}

	if frmVal, ok = req.Form["track"]; !ok {
		http.Error(resp, "track required", http.StatusBadRequest)
		return
	}
	if tr, err = strconv.Atoi(frmVal[0]); err != nil {
		http.Error(resp, "invalid track value", http.StatusBadRequest)
		return
	}

	if frmVal, ok = req.Form["user"]; !ok {
		http.Error(resp, "user required", http.StatusBadRequest)
		return
	}
	if usr, err = strconv.Atoi(frmVal[0]); err != nil {
		http.Error(resp, "invalid user value", http.StatusBadRequest)
		return
	}

	if !afl.checkAudioOwner(tr, resp) {
		return
	}

	_, err = afl.DB.Exec(`INSERT INTO share (id_audio, id_user) VALUES ($1, $2)`, tr, usr)
	if err != nil {
		if pgErr, ok := err.(*pq.Error); ok {
			switch pgErr.Code {
			case "23503": // foreign key constraint violation
				http.Error(resp, "user not exists", http.StatusBadRequest)
			default:
				http.Error(resp, pgErr.Detail, http.StatusBadRequest)
			}
			log.Println("Audio.Share query failed:", pgErr.Message, pgErr.Detail)
		} else {
			http.Error(resp, "internal error", http.StatusInternalServerError)
			log.Println("Audio.Share query failed:", err.Error())
		}
		return
	}

	resp.WriteHeader(http.StatusOK)
	resp.Write([]byte(""))
}

//Lock отменить “шаринг” аудиозаписи. Метод POST, доступен только авторизованным пользователям
//Параметры: track — id аудиозаписи, к доступ которой блокируется
//	user — пользователь, которому блокируется доступ
//Результат:
//Ошибка:
func (afl *Audiofill) Lock(resp http.ResponseWriter, req *http.Request) {
	var (
		err     error
		frmVal  []string
		ok      bool
		tr, usr int
	)
	if req.Method != http.MethodPost {
		http.Error(resp, "bad method", http.StatusMethodNotAllowed)
		return
	}

	if afl.userID, err = checkSession(afl.DB, req); err != nil {
		http.Error(resp, "access denied", http.StatusUnauthorized)
		return
	}

	if err = req.ParseForm(); err != nil {
		http.Error(resp, "wrong form data", http.StatusBadRequest)
		return
	}
	if frmVal, ok = req.Form["track"]; !ok {
		http.Error(resp, "track required", http.StatusBadRequest)
		return
	}
	if tr, err = strconv.Atoi(frmVal[0]); err != nil {
		http.Error(resp, "invalid track value", http.StatusBadRequest)
		return
	}

	if frmVal, ok = req.Form["user"]; !ok {
		http.Error(resp, "user required", http.StatusBadRequest)
		return
	}
	if usr, err = strconv.Atoi(frmVal[0]); err != nil {
		http.Error(resp, "invalid user value", http.StatusBadRequest)
		return
	}

	if !afl.checkAudioOwner(tr, resp) {
		return
	}

	qr, err := afl.DB.Exec(`DELETE FROM share WHERE id_audio = $1 AND id_user = $2`, tr, usr)
	if err != nil {
		if pgErr, ok := err.(*pq.Error); ok {
			http.Error(resp, pgErr.Detail, http.StatusInternalServerError)
			log.Println("Audio.Lock query failed:", pgErr.Message, pgErr.Detail)
		} else {
			http.Error(resp, "internal error", http.StatusInternalServerError)
			log.Println("Audio.Lock query failed:", err.Error())
		}
		return
	}
	if res, _ := qr.RowsAffected(); res == 0 {
		http.Error(resp, "no rows are deleted", http.StatusNotFound)
		return
	}
	resp.WriteHeader(http.StatusOK)
	resp.Write([]byte(""))
}

//Get получить файл с аудиозаписью. Метод GET, доступен только авторизованным пользователям
//Параметры: track — id аудиозаписи
//Результат:
//Ошибка:
func (afl *Audiofill) Get(resp http.ResponseWriter, req *http.Request) {
	var (
		err    error
		frmVal []string
		ok     bool
		tr     int
		qr     *sql.Row
		fd     *os.File

		fileDescr, fileName string
	)
	if req.Method != http.MethodGet {
		http.Error(resp, "bad method", http.StatusMethodNotAllowed)
		return
	}

	if afl.userID, err = checkSession(afl.DB, req); err != nil {
		http.Error(resp, "access denied", http.StatusUnauthorized)
		return
	}
	if err = req.ParseForm(); err != nil {
		http.Error(resp, "wrong form data", http.StatusBadRequest)
		return
	}
	if frmVal, ok = req.Form["track"]; !ok {
		http.Error(resp, "track required", http.StatusBadRequest)
		return
	}
	if tr, err = strconv.Atoi(frmVal[0]); err != nil {
		http.Error(resp, "track invalid value", http.StatusBadRequest)
		return
	}

	qr = afl.DB.QueryRow(`SELECT description, filename FROM audio a
		WHERE id_audio = $2 AND (id_owner = $1
			OR exists (SELECT id_audio FROM share s
				WHERE s.id_audio = a.id_audio AND s.id_user = $1)
			)
		`, afl.userID, tr)
	if err = qr.Scan(&fileDescr, &fileName); err != nil {
		http.Error(resp, "internal error", http.StatusInternalServerError)
		if pgErr, ok := err.(*pq.Error); ok {
			log.Println("Audio.Get query failed:", pgErr.Message, pgErr.Detail)
		} else {
			log.Println("Audio.Get query failed:", err.Error())
		}
		return
	}

	if fd, err = os.Open(path.Join(mediaDir, fileName)); err != nil {
		http.Error(resp, "file not found", http.StatusNotFound)
		return
	}

	defer fd.Close()
	http.ServeContent(resp, req, fileDescr, time.Now(), fd)
}

//Add добавить новую аудиозапись. Метод PUT. Доступен только авторизованным пользователям
//Параметры: file обязательный; name, duration — необязательные, по умолчанию
//	name = file.Filename, duration = '00:00'
//Результат:
//Ошибка:
func (afl *Audiofill) Add(resp http.ResponseWriter, req *http.Request) {
	var (
		frmVal []string
		isSet  bool
		err    error

		sqlQuery string
		sqlParam []interface{}
		tmpFile  *os.File
	)

	if req.Method != http.MethodPut {
		http.Error(resp, "bad method", http.StatusMethodNotAllowed)
		return
	}

	if afl.userID, err = checkSession(afl.DB, req); err != nil {
		http.Error(resp, "access denied", http.StatusUnauthorized)
		return
	}

	if err = req.ParseMultipartForm(2 << 10); err != nil {
		http.Error(resp, "wrong form data", http.StatusBadRequest)
		return
	}

	sqlQuery = `INSERT INTO audio (id_audio, id_owner, filename, description, duration)
		VALUES (default, $1, $2, $3, `
	sqlParam = append(sqlParam, afl.userID)

	fd, fh, err := req.FormFile("file")
	if err != nil {
		http.Error(resp, "file upload error", http.StatusBadRequest)
		return
	}
	defer fd.Close()
	if tmpFile, err = ioutil.TempFile(mediaDir, ""); err != nil {
		http.Error(resp, "iternal error", http.StatusInternalServerError)
		log.Println("Audio.Add temp file creating error:", err.Error())
		return
	}

	io.Copy(tmpFile, fd)
	if err = tmpFile.Close(); err != nil {
		http.Error(resp, "iternal error", http.StatusInternalServerError)
		log.Println("Audio.Add temp file creating error:", err.Error())
		return
	}
	sqlParam = append(sqlParam, path.Base(tmpFile.Name()))

	if frmVal, isSet = req.MultipartForm.Value["name"]; isSet {
		sqlParam = append(sqlParam, frmVal[0])
	} else {
		sqlParam = append(sqlParam, fh.Filename)
	}

	if frmVal, isSet = req.MultipartForm.Value["duration"]; isSet {
		sqlParam = append(sqlParam, frmVal[0])
		sqlQuery += "$4"
	} else {
		sqlQuery += "default"
	}
	sqlQuery += ")"

	_, err = afl.DB.Exec(sqlQuery, sqlParam...)
	if err != nil {
		//	rollback — delete temp file from mediaDir
		os.Remove(tmpFile.Name())
		if pgErr, ok := err.(*pq.Error); ok {
			http.Error(resp, pgErr.Detail, http.StatusInternalServerError)
			log.Println("Audio.Add query failed:", pgErr.Message, pgErr.Detail)
		} else {
			http.Error(resp, "internal error", http.StatusInternalServerError)
			log.Println("Audio.Add query failed:", err.Error())
		}
		return
	}

	resp.WriteHeader(http.StatusOK)
}

//copyAudio глубокое копирование структуры tAudio из src в dst
func (afl *Audiofill) copyAudio(dst, src *tAudio) {
	dst.AudioID, dst.Descr, dst.IsOwn, dst.OwnerID, dst.OwnerName = src.AudioID, src.Descr, src.IsOwn, src.OwnerID, src.OwnerName
	for _, v := range src.Shared {
		sh := &tShare{}
		sh.UserID, sh.UserName = v.UserID, v.UserName
		dst.Shared = append(dst.Shared, sh)
	}
}

//appendShare добваление в список Shared структуры tAudio ненулевых (не NULL) значений
func (afl *Audiofill) appendShare(dst *tAudio, id sql.NullInt64, name sql.NullString) {
	if id.Valid {
		dst.Shared = append(dst.Shared, &tShare{UserID: int(id.Int64), UserName: name.String})
	}
}

//checkAudioOwner проверка владельца трека id. Отдельный запрос нужне исключительно для
//	возврата статуса Forbidden.
//	Вообще, это условие можно проверить непосредственно при обновлении/вставке/удалении
//
func (afl *Audiofill) checkAudioOwner(id int, resp http.ResponseWriter) (ok bool) {
	qr := afl.DB.QueryRow(`SELECT id_owner = $1 FROM audio WHERE id_audio = $2`, afl.userID, id)
	if err := qr.Scan(&ok); err != nil {
		if err == sql.ErrNoRows {
			http.Error(resp, "track not found", http.StatusNotFound)
		} else {
			http.Error(resp, "Audio.Share qurey failed", http.StatusInternalServerError)
		}
		return false
	}
	if !ok {
		http.Error(resp, "access denied", http.StatusForbidden)
	}
	return ok
}

func main() {
	var (
		db  *sql.DB
		err error
	)

	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalln("Unable to connect to database. Check the connection settings in 'conf.go'", err)
		return
	}
	err = db.Ping()
	if err != nil {
		log.Fatalln("Unable to connect to database. Check the connection settings in 'conf.go'", err)
		return
	}

	ad := NewAudiofill(db)
	usr := NewUsers(db)
	mux := http.NewServeMux()
	mux.HandleFunc("/registration", usr.Registration)
	mux.HandleFunc("/login", usr.Login)
	mux.HandleFunc("/user/list", usr.List)
	mux.HandleFunc("/user/share", usr.Share)
	mux.HandleFunc("/audio/list", ad.List)
	mux.HandleFunc("/audio/share", ad.Share)
	mux.HandleFunc("/audio/lock", ad.Lock)
	mux.HandleFunc("/audio/get", ad.Get)
	mux.HandleFunc("/audio/add", ad.Add)

	fmt.Println("Server listen on :8008")
	http.ListenAndServe(":8008", mux)
}
