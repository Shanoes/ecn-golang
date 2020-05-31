# ecn-golang
Go lang test / demo application for Modicon BMEECN0100

Example use:
./monitor -endpoint opc.tcp://192.168.1.200:4840 -nodes "TANK1_LEVEL,TANK2_LEVEL,DEMO_STEP,TANK1_PRESSURE,TANK2_PRESSURE" -interval 600ms
./monitor -endpoint opc.tcp://192.168.1.200:4840 -prefix "ns=2;s=0" -nodes "TANK1_LEVEL,TANK2_LEVEL,DEMO_STEP,TANK1_PRESSURE,TANK2_PRESSURE" -interval 600ms

Due to nature of Modicon OPC server, intervals less than 500ms are sketchy. Intervals less than 350ms will not work and will crash the program.
Float datatypes only handled in this version
