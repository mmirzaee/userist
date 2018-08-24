package rest

import (
	"github.com/gorilla/mux"
	"github.com/gorilla/handlers"
	"github.com/spf13/viper"
	log "github.com/sirupsen/logrus"
	"net/http"
	"time"
	"strconv"
	"encoding/json"
	"github.com/dgrijalva/jwt-go"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"errors"
	"github.com/mmirzaee/userist/models"
	"os"
	"net/http/httputil"
)

func Serve() {
	httpServerConfig := viper.GetStringMap("http_server")

	r := mux.NewRouter()
	SetRoutes(r);
	r.Handle("/auth/login", handlers.LoggingHandler(os.Stdout, http.DefaultServeMux))
	srv := &http.Server{
		Handler: handlers.CORS(
			handlers.AllowedOrigins([]string{"*"}),
			handlers.AllowedMethods([]string{"POST", "OPTIONS", "GET"}),
			handlers.AllowedHeaders([]string{"Content-Type", "X-Requested-With", "x-tenant-id", "Authorization"}),
		)(r),
		Addr: httpServerConfig["host"].(string) + ":" + strconv.Itoa(httpServerConfig["port"].(int)),

		// Good practice: enforce timeouts for servers you create!
		WriteTimeout: 30 * time.Second,
		ReadTimeout:  30 * time.Second,
	}

	log.Fatal(srv.ListenAndServe())
}

func interceptor(f func(http.ResponseWriter, *http.Request, AuthorizedUser, int), checkAuth bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		AddDefaultHeaders(w)

		u := AuthorizedUser{}
		t := 0

		// Check Auth
		if checkAuth {
			user, err := CheckAuth(r)
			if err != nil {
				JsonHttpRespond(w, nil, err.Error(), http.StatusUnauthorized)
				return
			}
			u = user

			// Check tenant
			tenantID, errBadTenantID := strconv.Atoi(r.Header.Get("x-tenant-id"))
			if errBadTenantID != nil || tenantID <= 0 {
				JsonHttpRespond(w, nil, "x-tenant-id header is not set", http.StatusForbidden)
				return
			}
			t = tenantID
		}

		logConfig := viper.GetStringMap("log")
		if logConfig["enable_http_requests_log"] == true {
			requestDump, err := httputil.DumpRequest(r, true)
			if err != nil {
				log.Error(err)
			}
			log.Info("Request: \n" + string(requestDump))
		}

		// Call Original
		f(w, r, u, t)

	})
}

func SetRoutes(r *mux.Router) {
	r.Handle("/roles", interceptor(GetRoles, true)).Methods("GET")
	r.Handle("/tenants", interceptor(GetTenants, true)).Methods("GET")
	r.Handle("/tenants", interceptor(PostTenants, true)).Methods("POST")
	r.Handle("/tenants/{id}", interceptor(UpdateTenant, true)).Methods("POST")
	r.Handle("/tenants/{id}", interceptor(DeleteTenant, true)).Methods("DELETE")
	r.Handle("/auth/login", interceptor(PostAuthLogin, false)).Methods("POST")
	r.Handle("/auth/refresh-token", interceptor(PostRefreshToken, true)).Methods("POST")
	r.Handle("/auth/check-token", interceptor(PostAuthCheckToken, true)).Methods("POST")
	r.Handle("/users", interceptor(PostUsers, true)).Methods("POST")
	r.Handle("/users/{id}", interceptor(PostUpdateUser, true)).Methods("POST")
	r.Handle("/users", interceptor(GetUsers, true)).Methods("GET")
	r.Handle("/users/{id}", interceptor(GetSingleUser, true)).Methods("GET")
	r.Handle("/users/{id}/meta/{key}", interceptor(GetSingleUserMeta, true)).Methods("GET")
	r.Handle("/users/{id}/umeta/{key}", interceptor(GetSingleUserMeta, true)).Methods("GET")
	r.Handle("/users/{id}/meta/{key}", interceptor(UpdateSingleUserMeta, true)).Methods("POST")
	r.Handle("/users/{id}/umeta/{key}", interceptor(UpdateSingleUniqueUserMeta, true)).Methods("POST")
	r.Handle("/users/{id}/permissions", interceptor(UpdateUserPermissions, true)).Methods("POST")
	r.Handle("/users/{id}/tenants", interceptor(GetUserTenants, true)).Methods("GET")
	r.Handle("/users/{id}", interceptor(DeleteUser, true)).Methods("DELETE")
	r.Handle("/users/{id}/meta/{key}", interceptor(DeleteUserMeta, true)).Methods("DELETE")
	r.Handle("/users/{id}/permissions", interceptor(DeleteUserTenantRole, true)).Methods("DELETE")
}

func GenerateToken(user_data TokenData) string {
	jwtConfig := viper.GetStringMap("jwt")
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"uid": user_data.UserId,
		"pms": user_data.Roles,
		"exp": time.Now().Add(time.Duration(jwtConfig["lifetime"].(int)) * time.Second).Unix(),
		"iat": time.Now().Unix(),
	})

	signingKey := []byte(jwtConfig["secret"].(string))

	// Sign and get the complete encoded token as a string using the secret
	tokenString, err := token.SignedString(signingKey)

	if err != nil {
		log.Error(err)
	}
	return tokenString
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		log.Error(err)
	}
	return err == nil
}

func HasPermission(user *AuthorizedUser, permission string, tenant_id int) bool {
	hasPerm := false
	if user.Type == "user" {
		tenants := user.Permissions.(map[string]interface{})
		tenant, tenantExists := tenants[strconv.Itoa(tenant_id)];
		if tenantExists {
			for _, perm := range tenant.([]interface{}) {
				if perm == permission {
					hasPerm = true
				}
			}
		}
	} else if user.Type == "service" {
		for _, perm := range user.Permissions.([]interface{}) {
			if perm.(string) == permission {
				hasPerm = true
			}
		}
	}
	return hasPerm
}

func JsonHttpRespond(w http.ResponseWriter, respond interface{}, error string, status int) {
	w.WriteHeader(status)

	logConfig := viper.GetStringMap("log")
	if logConfig["enable_http_requests_log"] == true {

		if error != "" {
			res, _ := json.Marshal(map[string]string{"error": error})
			log.Error("Status: " + strconv.Itoa(status) + ", Response: " + string(res))
		} else {
			res, _ := json.Marshal(respond)
			log.Info("Status: " + strconv.Itoa(status) + ", Response: " + string(res))
		}
	}

	if error != "" {
		json.NewEncoder(w).Encode(map[string]string{"error": error})
		return
	}
	json.NewEncoder(w).Encode(respond)
	return
}

func AddDefaultHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func CheckAuth(r *http.Request) (AuthorizedUser, error) {
	authHeader := r.Header.Get("Authorization")
	if strings.Contains(authHeader, "Bearer") {
		authHeader = strings.Replace(authHeader, "Bearer", "", -1)
		tokenString := strings.TrimSpace(authHeader)

		// Parse the token
		jwtConfig := viper.GetStringMap("jwt")

		token, err := jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
			signingKey := []byte(jwtConfig["secret"].(string))
			return signingKey, nil
		})

		if err != nil {
			log.Error(err)
			return AuthorizedUser{}, err
		}

		if token.Valid {
			claims := token.Claims.(jwt.MapClaims)
			user, err := models.GetUserByID(uint(claims["uid"].(float64)))
			if err != nil {
				log.Error(err)
				return AuthorizedUser{}, err
			}

			return AuthorizedUser{User: *user, Permissions: claims["pms"], Type: "user"}, nil
		}
	} else {
		if viper.IsSet("services_auth_keys") {
			services := viper.Get("services_auth_keys")
			for _, s := range services.([]interface{}) {
				service_key := s.(map[interface{}]interface{})["key"].(string)
				if authHeader == service_key {
					service_name := s.(map[interface{}]interface{})["name"].(string)
					service_perms := s.(map[interface{}]interface{})["permissions"].([]interface{})
					return AuthorizedUser{User: models.User{DisplayName: service_name, Username: service_name, Status: 1}, Permissions: service_perms, Type: "service"}, nil
				}
			}
		}
		return AuthorizedUser{}, errors.New("invalid token")
	}

	return AuthorizedUser{}, errors.New("invalid token")

}
