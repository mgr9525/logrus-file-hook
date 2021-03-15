# logrus-file-hook
logrus file size limit hook.
> logrus 日志文件按大小分割 hook



# test:
## see [log_test.go](log_test.go)

```
    dir := "logs"
	pmp := PathMap{
		logrus.InfoLevel:  filepath.Join(dir, "info.log"),
		logrus.ErrorLevel: filepath.Join(dir, "error.log"),
		logrus.DebugLevel: filepath.Join(dir, "debug.log"),
	}
	logrus.SetLevel(logrus.DebugLevel)
	logrus.AddHook(NewLfsHook(pmp, &logrus.TextFormatter{}, 1024 /*max file size 1Kb*/,5 /*max file count*/))

	for i := 0; i < 1024; i++ {
		logrus.Errorf("this is err!")
		logrus.Debugf("this is debug")
		logrus.Infof("this is info")
	}
```