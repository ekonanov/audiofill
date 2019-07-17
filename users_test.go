package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

type testUser struct {
	Method string
	Path   string
	Query  string
	Cookie *http.Cookie
	Status int
	Error  string
	Body   tUsrList
}

var (
	testSrv *httptest.Server
	junkSrv *httptest.Server
)

func (usr tUsrList) String() (s string) {
	s = fmt.Sprintf("Count: %d,\nList: [\n", usr.Count)
	for _, v := range usr.List {
		s += fmt.Sprintf("\t{UserID: %d,\tName: %s,\tLogin: %s,\tShared: %d},\n", v.UserID, v.Name, v.Login, v.Shared)
	}
	s += "]\n"
	return s
}

func TestUserRegistred(t *testing.T) {
	// TestUserRegistred тестирование регистрации нового пользователя
	var (
		req  *http.Request
		resp *http.Response
		err  error

		client   *http.Client
		testName string
	)

	client = http.DefaultClient

	tests := []testUser{
		//******** РЕГИСТРАЦИЯ **********
		testUser{ //	0 недопустимый метод
			Method: http.MethodGet,
			Path:   "/registration",
			Status: http.StatusMethodNotAllowed,
			Error:  "bad method\n",
		},
		testUser{ //	1 отсутствуют обязательные параметры
			Method: http.MethodPut,
			Path:   "/registration",
			Status: http.StatusBadRequest,
			Error:  "login required\n",
		},
		testUser{ //	2 отсутствуют обязательные параметры
			Method: http.MethodPut,
			Path:   "/registration",
			Query:  "login=user",
			Status: http.StatusBadRequest,
			Error:  "password required\n",
		},
		testUser{ //	3 успешная регистрация
			Method: http.MethodPut,
			Path:   "/registration",
			Query:  "login=noname&passwd=123",
			Status: http.StatusCreated,
		},
		testUser{ //	4 успешная регистрация с именем
			Method: http.MethodPut,
			Path:   "/registration",
			Query:  "login=hunter&passwd=123456&name=Ghost%20Buster",
			Status: http.StatusCreated,
		},
		testUser{ //	5 повторная регистрация
			Method: http.MethodPut,
			Path:   "/registration",
			Query:  "login=admin&passwd=othersecret",
			Status: http.StatusBadRequest,
			Error:  "login already used\n",
		},
		// ********** АВТОРИЗАЦИЯ ***********
		testUser{ //	6 недопустимый метод
			Method: http.MethodGet,
			Path:   "/login",
			Status: http.StatusMethodNotAllowed,
			Error:  "bad method\n",
		},
		testUser{ //	7 отсутствуют обязательные параметры
			Method: http.MethodPost,
			Path:   "/login",
			Status: http.StatusBadRequest,
			Error:  "login required\n",
		},
		testUser{ //	8 отсутствуют обязательные параметры
			Method: http.MethodPost,
			Path:   "/login",
			Query:  "login=admin",
			Status: http.StatusBadRequest,
			Error:  "password required\n",
		},
		testUser{ //	9 неуспешная авторизация
			Method: http.MethodPost,
			Path:   "/login",
			Query:  "login=user&passwd=wrongpwd",
			Status: http.StatusNotFound,
			Error:  "wrong login or password\n",
		},
		testUser{ //	10 успешная авторизация нового
			Method: http.MethodPost,
			Path:   "/login",
			Query:  "login=noname&passwd=123",
			Status: http.StatusOK,
		},
		testUser{ //	11 попытка завершения отсутствующей сессии
			Method: http.MethodPost,
			Path:   "/logout",
			Cookie: &http.Cookie{Name: "session_id", Value: "12345678901234567890123456789012"},
			Status: http.StatusOK,
		},
	}

	for idx, tst := range tests {
		testName = fmt.Sprintf("User.Registration: test [%d] %s ? %s", idx, tst.Method, tst.Query)

		if tst.Method == http.MethodGet {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path+"?"+tst.Query, nil)
		} else {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path, strings.NewReader(tst.Query))
			req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		}

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("%s >> query fail %s", testName, err.Error())
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != tst.Status {
			t.Errorf("%s >>> wrong status %d, expected %d\n", testName, resp.StatusCode, tst.Status)
			continue
		}

		if !((resp.StatusCode == http.StatusOK) || (resp.StatusCode == http.StatusCreated)) {
			respBody, _ := ioutil.ReadAll(resp.Body)
			if tst.Error != string(respBody) {
				t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, respBody, tst.Body)
				continue
			}
		}
	}

	//	особые случаи ошибок: вызов POST без параметров (nil)
	resp, err = http.Post(testSrv.URL+"/login", "login=guest&passwd=123", nil)
	if err != nil {
		t.Fatalf("Users.Login test bad parameters >> query fail %s", err.Error())

	} else if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Users.Login test bad parameters >>> wrong status %d, expected %d\n", resp.StatusCode, http.StatusBadRequest)

	} else {
		respBody, _ := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if string(respBody) != "wrong form data\n" {
			t.Errorf("Users.Login test bad parameters >>> wrong body [%s], expected [wrong form data\n]\n", respBody)
		}
	}

	//	повторная авторизация (есть устаревшая сессия)
	cookSess := []*http.Cookie{}
	resp, err = http.Post(testSrv.URL+"/login", "application/x-www-form-urlencoded", strings.NewReader("login=noname&passwd=123"))
	if err != nil {
		t.Fatalf("Users.Login test bad parameters >> query fail %s", err.Error())

	} else if resp.StatusCode != http.StatusOK {
		t.Errorf("Users.Login test bad parameters >>> wrong status %d, expected %d\n", resp.StatusCode, http.StatusOK)

	} else {
		cookSess = resp.Cookies()
	}

	//	закрытие сессии
	req, err = http.NewRequest(http.MethodPost, testSrv.URL+"/logout", nil)
	for _, c := range cookSess {
		req.AddCookie(c)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Error(err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Users.Logout test >>> wrong status %s, expected %d", resp.Status, http.StatusOK)
	}
}

func TestUserList(t *testing.T) {
	var (
		client   *http.Client
		sessCook *http.Cookie
		testName string
		err      error
		req      *http.Request
		resp     *http.Response
		result   tUsrList
	)
	// Получение списков пользователей
	client = http.DefaultClient
	sessCook = &http.Cookie{Name: "session_id", Value: "3d73274ac8b18ab09528075c7fee1213"}

	testes := []testUser{
		// ********** ПОЛНЫЙ СПИСОК ***********
		testUser{ //	0 недопустимый метод
			Method: http.MethodPut,
			Path:   "/user/list",
			Status: http.StatusMethodNotAllowed,
			Error:  "bad method\n",
		},
		testUser{ //	1 отсутствуют параметры, д.б. по умолчанию
			Method: http.MethodGet,
			Path:   "/user/list",
			Cookie: sessCook,
			Status: http.StatusOK,
			Body: tUsrList{
				List: []*tUser{
					&tUser{UserID: 1, Name: "", Login: "admin"},
					&tUser{UserID: 2, Name: "Lorem Ipsum", Login: "user"},
					&tUser{UserID: 3, Name: "Uninvited T", Login: "guest"},
					&tUser{UserID: 4, Name: "Dutchman Flying", Login: "ghost"},
					&tUser{UserID: 5, Name: "", Login: "noname"},
					&tUser{UserID: 6, Name: "Ghost Buster", Login: "hunter"},
				},
			},
		},
		testUser{ //	2 ошибочные параметры, д.б. исправлены
			Method: http.MethodGet,
			Path:   "/user/list",
			Cookie: sessCook,
			Query:  "page_no=-1&on_page=-5",
			Status: http.StatusOK,
			Body: tUsrList{
				List: []*tUser{
					&tUser{UserID: 1, Name: "", Login: "admin"},
					&tUser{UserID: 2, Name: "Lorem Ipsum", Login: "user"},
					&tUser{UserID: 3, Name: "Uninvited T", Login: "guest"},
					&tUser{UserID: 4, Name: "Dutchman Flying", Login: "ghost"},
					&tUser{UserID: 5, Name: "", Login: "noname"},
					&tUser{UserID: 6, Name: "Ghost Buster", Login: "hunter"},
				},
			},
		},
		testUser{ //	3 ошибочные параметры, д.б. исправлены
			Method: http.MethodGet,
			Path:   "/user/list",
			Cookie: sessCook,
			Query:  "page_no=ab&on_page=cd",
			Status: http.StatusOK,
			Body: tUsrList{
				List: []*tUser{
					&tUser{UserID: 1, Name: "", Login: "admin"},
					&tUser{UserID: 2, Name: "Lorem Ipsum", Login: "user"},
					&tUser{UserID: 3, Name: "Uninvited T", Login: "guest"},
					&tUser{UserID: 4, Name: "Dutchman Flying", Login: "ghost"},
					&tUser{UserID: 5, Name: "", Login: "noname"},
					&tUser{UserID: 6, Name: "Ghost Buster", Login: "hunter"},
				},
			},
		},
		testUser{ //	4 полные параметры
			Method: http.MethodGet,
			Path:   "/user/list",
			Cookie: sessCook,
			Query:  "page_no=2&on_page=2",
			Status: http.StatusOK,
			Body: tUsrList{
				List: []*tUser{
					&tUser{UserID: 3, Name: "Uninvited T", Login: "guest"},
					&tUser{UserID: 4, Name: "Dutchman Flying", Login: "ghost"},
				},
			},
		},
		testUser{ //	5 параметры за пределами списка пользователей
			Method: http.MethodGet,
			Path:   "/user/list",
			Cookie: sessCook,
			Query:  "page_no=3",
			Status: http.StatusNotFound,
			Error:  "\n",
		},
		// ********* СПИСОК РАСШАРЕННЫХ *************
		testUser{ //	6 недопустимый метод
			Method: http.MethodPut,
			Path:   "/user/share",
			Status: http.StatusMethodNotAllowed,
			Error:  "bad method\n",
		},
		testUser{ //	7 отсутствуют параметры, д.б. по умолчанию
			Method: http.MethodGet,
			Path:   "/user/share",
			Cookie: sessCook,
			Status: http.StatusOK,
			Body: tUsrList{
				Count: 2,
				List: []*tUser{
					&tUser{UserID: 1, Name: "admin", Shared: 2},
					&tUser{UserID: 2, Name: "Lorem Ipsum", Shared: 1},
				},
			},
		},
		testUser{ //	8 ошибочные параметры, д.б. исправлены
			Method: http.MethodGet,
			Path:   "/user/share",
			Cookie: sessCook,
			Query:  "page_no=-1",
			Status: http.StatusOK,
			Body: tUsrList{
				Count: 2,
				List: []*tUser{
					&tUser{UserID: 1, Name: "admin", Shared: 2},
					&tUser{UserID: 2, Name: "Lorem Ipsum", Shared: 1},
				},
			},
		},
		testUser{ //	9 полные параметры
			Method: http.MethodGet,
			Path:   "/user/share",
			Cookie: sessCook,
			Query:  "page_no=1&on_page=2",
			Status: http.StatusOK,
			Body: tUsrList{
				Count: 2,
				List: []*tUser{
					&tUser{UserID: 1, Name: "admin", Shared: 2},
					&tUser{UserID: 2, Name: "Lorem Ipsum", Shared: 1},
				},
			},
		},
		testUser{ //	10 параметры за пределами списка пользователей
			Method: http.MethodGet,
			Path:   "/user/share",
			Cookie: sessCook,
			Query:  "page_no=3",
			Status: http.StatusNotFound,
			Error:  "\n",
		},
	}

	for idx, tst := range testes {
		testName = fmt.Sprintf("User.List: test [%d] %s ? %s", idx, tst.Method, tst.Query)

		if tst.Method == http.MethodGet {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path+"?"+tst.Query, nil)
		} else {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path, strings.NewReader(tst.Query))
			req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		}
		if tst.Cookie != nil {
			req.AddCookie(tst.Cookie)
		}

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("%s >> query fail %s", testName, err.Error())
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != tst.Status {
			t.Errorf("%s >>> wrong status %d, expected %d\n", testName, resp.StatusCode, tst.Status)
			continue
		}

		respBody, _ := ioutil.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK {
			result = tUsrList{}
			err = json.Unmarshal(respBody, &result)
			if err != nil {
				t.Errorf("%s >>> unmarshaling result error [%s]", testName, err.Error())
				continue
			}
			if !reflect.DeepEqual(tst.Body, result) {
				t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, result.String(), tst.Body.String())
			}

		} else if tst.Error != string(respBody) {
			t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, respBody, tst.Error)
			continue
		}
	}
}
func TestMain(m *testing.M) {
	var (
		err error
		db  *sql.DB

		usr *Users
		ad  *Audiofill
		mux *http.ServeMux
	)

	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("cant open db:", err)
	}

	err = db.Ping()
	if err != nil {
		log.Fatal("cant connect to db", err)
	}

	_, err = db.Exec(pgDump)
	if err != nil {
		log.Fatal("database dump failed ", err)
	}

	usr = NewUsers(db)
	ad = NewAudiofill(db)
	mux = http.NewServeMux()
	mux.HandleFunc("/registration", usr.Registration)
	mux.HandleFunc("/login", usr.Login)
	mux.HandleFunc("/logout", usr.Logout)
	mux.HandleFunc("/user/list", usr.List)
	mux.HandleFunc("/user/share", usr.Share)
	mux.HandleFunc("/audio/list", ad.List)
	mux.HandleFunc("/audio/share", ad.Share)
	mux.HandleFunc("/audio/lock", ad.Lock)
	mux.HandleFunc("/audio/get", ad.Get)
	mux.HandleFunc("/audio/add", ad.Add)
	testSrv = httptest.NewServer(mux)
	defer testSrv.Close()

	codeRun := m.Run()
	db.Close()
	os.Exit(codeRun)
}
