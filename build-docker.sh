docker build -t bosbase:ve1 .
docker build -t bosbase:ve1 -f docker/Dockerfile .
docker tag bosbase:ve1 bosbase/bosbase:ve1
docker push bosbase/bosbase:ve1