docker build -t bosbase:ve1 .
docker build -t bosbase:ve1 -f docker/Dockerfile .
docker tag bosbase:ve1 bosbase/bosbase:ve1
docker push bosbase/bosbase:ve1





docker build -t bosbase:0.0.2 -f docker/Dockerfile .
docker tag bosbase:0.0.2 bosbase/bosbase:0.0.2
docker tag bosbase:0.0.2 bosbase/bosbase:latest
docker push bosbase/bosbase:0.0.2
docker push bosbase/bosbase:latest