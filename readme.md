# DAEMON

Install app as system service [support Linux, Darwin, FreeBSD and Windows]

Precompiled files in /build

daemon-darwin-amd64<br />
daemon-darwin-arm64<br />
daemon-freebsd-amd64<br />
daemon-freebsd-arm64<br />
daemon-windows-amd64<br />
daemon-windows-386<br />
daemon-windows-arm64<br />
daemon-linux-amd64<br />
daemon-linux-386<br />
daemon-linux-arm64<br />
daemon-linux-arm32<br />


## How to use
1. Compile program according to the os-arch system. 
2. Copy the compiled file into your project package and rename to "daemon".
3. Run `sudo ./daemon install <service-name> <app> [app arguments...]` to install a service.
4. Manage it with `start`, `stop`, `restart`, `status`, or `remove` using the service name.

### for example
```
├─{your-project-folder}
│  ├─configs    //cofig folder
│  ├─logs       //log folder
│  ├─assets     //assets folderr
│  ├─myapp      //executable file
│  └─daemon    //daemon file compiled and copy from this package
```

The service name is explicit and independent from the executable filename. It may contain `A-Z`, `a-z`, `0-9`, `.`, `_`, `@`, and `-`. Internally, registrations use the reserved `lz_lz_` prefix; commands and list output hide this implementation detail. A relative executable name is resolved beside the daemon binary. An executable in another folder can be installed using its absolute path.


run cmd
```
//enter {your-project-folder}
cd ./{your-project-folder}

sudo ./daemon install my-service myapp [arg1] [arg2] ...
sudo ./daemon install my-service myapp --port 8080 --config "configs/my app.toml"
sudo ./daemon install my-service "/opt/My App/myapp" --port 8080 --config "/opt/My App/config.toml"
sudo ./daemon list
sudo ./daemon ls
sudo ./daemon start my-service
sudo ./daemon status my-service
sudo ./daemon restart my-service
sudo ./daemon stop my-service
sudo ./daemon remove my-service
```

`list` and `ls` show each managed service and its current status:

```text
NAME        STATUS
api         stopped
my-service  running
```


### in the test folder there are test apps for different architecture

#### example you can run on arm64 op-system
```
sudo ./daemon install app-test app_test-linux-arm64
```
#### or run on arm32(armv6,armv7,etc..) op-system
```
sudo ./daemon install app-test app_test-linux-arm32
```





