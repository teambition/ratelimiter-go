test:
	go test --race

bench:
	go test -bench=.

cover:
	rm -f *.coverprofile
	go test -coverprofile=ratelimiter.coverprofile
	gover
	go tool cover -html=ratelimiter.coverprofile

.PHONY: test cover
