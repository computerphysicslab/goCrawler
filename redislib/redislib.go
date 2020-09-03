// Package redislib provides basic string & interface set/get/exists functions
package redislib

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/gomodule/redigo/redis"
)

/***************************************************************************************************************
****************************************************************************************************************
* Redis functions ************************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

// To establish connectivity in redigo, you need to create a redis.Pool object which is a pool of connections to Redis
func newPool() *redis.Pool {
	return &redis.Pool{
		// Max number of idle connections in the pool
		MaxIdle: 80,
		// Max number of connections
		MaxActive: 12000,
		// Dial is an application supplied function for creating and configuring a connection
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", ":6379")
			if err != nil {
				panic(err.Error())
			}
			return c, err
		},
	}
}

var pool *redis.Pool
var conn redis.Conn

// Use always same connection
func defaultConn() redis.Conn {
	if pool == nil { // Init
		fmt.Printf("Redis pool init\n")
		pool = newPool()
		conn = pool.Get()
	}
	return conn
}

// Ping tests connectivity for redis (PONG should be returned)
func Ping() error {
	// Send PING command to Redis
	pong, err := defaultConn().Do("PING")
	if err != nil {
		return err
	}

	// PING command returns a Redis "Simple String"
	s, err := redis.String(pong, err)
	if err != nil {
		return err
	}

	fmt.Printf("PING Response = %s\n", s)
	// Output: PONG

	return nil
}

// Set executes the redis SET command
func Set(key string, value string) error {
	_, err := defaultConn().Do("SET", key, value)
	if err != nil {
		return err
	}

	return nil
}

// SetInterface stores a structure
func SetInterface(key string, payload interface{}) error {
	value, err := json.Marshal(payload)
	if err != nil {
		log.Fatal("Redis setInterface error: ", err)
		panic(err)
	}

	return Set(key, string(value))
}

// Exists checks if a pair key/value has been previously stored on Redis
func Exists(key string) bool {
	_, err := redis.String(defaultConn().Do("GET", key))
	if err == redis.ErrNil {
		return false
	} else if err != nil {
		log.Fatal("Redis exists error: ", err)
	}

	return true
}

// Get executes the redis GET command
func Get(key string) string {
	s, err := redis.String(defaultConn().Do("GET", key))
	if err == redis.ErrNil {
		fmt.Printf("Redis get error: %s does not exist\n", key)
	} else if err != nil {
		log.Fatal("Redis get error: ", err)
	}

	return s
}

// GetInterface returns a structure by its key
func GetInterface(key string) interface{} {
	var payload interface{}

	value := Get(key)
	fmt.Printf("DEBUG: %s\n", value)
	err := json.Unmarshal([]byte(value), &payload)
	if err != nil {
		log.Fatal("Redis getInterface error: ", err)
		panic(err)
	}

	return payload
}

// Test shows package functionality
func Test() {
	// defer conn.Close()

	err := Ping()
	if err != nil {
		fmt.Println(err)
	}

	Set("Favorite Movie", "The Matrix")
	fmt.Printf("Key existence: %+v\n", Exists("Favorite Movie"))
	fmt.Printf("Redis output: %s\n", Get("Favorite Movie"))

	Set("Favorite City", "Tokyo")
	fmt.Printf("Key existence: %+v\n", Exists("Favorite City"))
	fmt.Printf("Redis output: %s\n", Get("Favorite City"))

	SetInterface("Favorite Languages", []string{"goLang", "python", "r", "c", "php", "perl", "solidity"})
	fmt.Printf("Key existence: %+v\n", Exists("Favorite Languages"))
	fmt.Printf("Redis output as string: %s\n", Get("Favorite Languages"))
	fmt.Printf("Redis output as structure: %+v\n", GetInterface("Favorite Languages"))
}
