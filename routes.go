package main

func (a *app) routes() {
	a.logger.Info("Setting up route handlers")
	a.router.HandleFunc("/", a.HandleRedirect).Methods("GET")
	a.router.HandleFunc("/v1", a.HandleIntrospection).Methods("GET")
	a.router.HandleFunc("/healthz", a.HandleHealthCheck).Methods("GET")
	a.router.HandleFunc("/v1/semesters/{semester}", a.HandleGetSemester).Methods("GET")
	a.router.HandleFunc("/v1/courses/{course}", a.HandleGetCourse).Methods("GET")
	a.router.HandleFunc("/v1/courses", a.HandleGetCourses).Methods("GET")
	a.router.HandleFunc("/v1/courses", a.HandleRefreshCourses).Methods("PATCH")
}
