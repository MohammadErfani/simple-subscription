package main

import (
	"database/sql"
	"encoding/gob"
	"errors"
	"fmt"
	"github.com/MohammadErfani/simple-subscription/data"
	"github.com/alexedwards/scs/redisstore"
	"github.com/alexedwards/scs/v2"
	"github.com/gomodule/redigo/redis"
	_ "github.com/jackc/pgx/v5/stdlib"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const webPort = "8000"

func main() {
	// connect to database
	db, err := ConnectToDB()
	if err != nil {
		log.Fatal(err)
	}
	// create sessions
	session := initSession()

	// create loggers
	infoLog := log.New(os.Stdout, "INFO\t", log.Ldate|log.Ltime)
	errLog := log.New(os.Stdout, "Error\t", log.Ldate|log.Ltime|log.Lshortfile)
	// create channels

	// create wait group
	wg := sync.WaitGroup{}
	// set up the application config
	app := Config{
		Session:  session,
		DB:       db,
		Wait:     &wg,
		InfoLog:  infoLog,
		ErrorLog: errLog,
		Models:   data.New(db),
	}
	// set up mail concurrently
	app.Mailer = app.createMail()
	go app.listenForMail()
	// listen for signals
	go app.listenForShutdown()
	// listen for web connections
	app.serve()

}

func (app *Config) serve() {
	srv := http.Server{
		Addr:    fmt.Sprintf(":%s", webPort),
		Handler: app.routes(),
	}
	app.InfoLog.Printf("starting server on %v", webPort)
	err := srv.ListenAndServe()
	if err != nil {
		app.ErrorLog.Fatal(err)
	}
}

func ConnectToDB() (*sql.DB, error) {
	counts := 10
	dsn := os.Getenv("DSN")
	for i := 1; i <= counts; i++ {
		db, err := sql.Open("pgx", dsn)
		if err == nil {
			err = db.Ping()
			if err != nil {
				return nil, err
			}
			log.Println("connect to database")
			return db, nil
		}
		log.Println("postgres not yet ready...", err)
		time.Sleep(1 * time.Second)
	}
	return nil, errors.New("cannot connect to database")
}

func initSession() *scs.SessionManager {
	session := scs.New()
	// for storing user in session
	gob.Register(data.User{})
	session.Store = redisstore.New(connectRedis())
	session.Lifetime = 24 * time.Hour
	session.Cookie.Persist = true
	session.Cookie.SameSite = http.SameSiteLaxMode
	session.Cookie.Secure = true
	return session
}

func connectRedis() *redis.Pool {
	redisPool := &redis.Pool{
		MaxIdle: 10,
		Dial: func() (redis.Conn, error) {
			return redis.Dial("tcp", os.Getenv("REDIS"))
		},
	}
	return redisPool
}

func (app *Config) listenForShutdown() {
	quit := make(chan os.Signal)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	app.shutdown()
	os.Exit(0)
}

func (app *Config) shutdown() {
	app.InfoLog.Println("run cleanup task")

	// wait until waitgroup is empty
	app.Wait.Wait()
	app.Mailer.DoneChan <- true
	app.InfoLog.Println("closing channels and shutting down application...")
	close(app.Mailer.MailerChan)
	close(app.Mailer.ErrorChan)
	close(app.Mailer.DoneChan)
}

func (app *Config) createMail() Mail {
	// create channels for mailer
	errorChan := make(chan error)
	mailChan := make(chan Message, 100)
	mailerDoneChan := make(chan bool)
	m := Mail{
		Domain:      "localhost",
		Host:        "localhost",
		Port:        1025,
		Encryption:  "none",
		FromAddress: "mohammad@erfani.com",
		FromName:    "mohammad",
		Wait:        app.Wait,
		ErrorChan:   errorChan,
		MailerChan:  mailChan,
		DoneChan:    mailerDoneChan,
	}
	return m
}
