######################
# desc 
#	old		老版本, 各种条件比较完善
#	./*.go  新版本, 放弃各种条件, 拉开Spread, 纯网格波动
#
#################



install:
	go get -u gopkg.in/yaml.v2
	go get -u github.com/google/uuid
	go get -u github.com/nubo/jwt
	go get -u github.com/huoxuhuoxu/GoEx
	go get -u github.com/huoxuhuoxu/UseGoexPackaging
	go get -u github.com/joho/godotenv
