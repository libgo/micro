package micro

import (
	"testing"
)

func Test_micro_genBind(t *testing.T) {
	type fields struct {
		name       string
		logger     Logger
		closeFuncs []func() error
		errChan    chan error
		serveFuncs []func()
	}
	type args struct {
		addr string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   string
	}{
		{
			args: args{addr: "9090"},
			want: ":9090",
		},
		{
			args: args{addr: ":9090"},
			want: ":9090",
		},
		{
			args: args{addr: "0.0.0.0:9090"},
			want: "0.0.0.0:9090",
		},
		{
			args: args{addr: "GRPC|9090"},
			want: ":9090",
		},
		{
			args: args{addr: "GRPC|:9090"},
			want: ":9090",
		},
		{
			args: args{addr: "GRPC|0.0.0.0:9090"},
			want: "0.0.0.0:9090",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &micro{
				name:       tt.fields.name,
				logger:     tt.fields.logger,
				closeFuncs: tt.fields.closeFuncs,
				errChan:    tt.fields.errChan,
				serveFuncs: tt.fields.serveFuncs,
			}
			if got := m.genBind(tt.args.addr); got != tt.want {
				t.Errorf("micro.genBind() = %v, want %v", got, tt.want)
			}
		})
	}
}
