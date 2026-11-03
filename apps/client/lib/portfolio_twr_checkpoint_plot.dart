import 'models.dart';

class TwrCheckpointPlot {
  TwrCheckpointPlot._(this.points, this.minimum, this.maximum, this.zeroY);
  factory TwrCheckpointPlot(List<TwrCheckpoint> source) {
    if (source.isEmpty) {
      throw ArgumentError('A plot requires checkpoints');
    }
    final values = source
        .map((point) => _scaled(point.cumulativeRate))
        .toList();
    var low = 0, high = 0;
    for (var index = 1; index < values.length; index++) {
      if (values[index] < values[low]) {
        low = index;
      }
      if (values[index] > values[high]) {
        high = index;
      }
    }
    final first = source.first.atNanoseconds, last = source.last.atNanoseconds;
    final span = values[high] - values[low];
    final points = [
      for (var index = 0; index < source.length; index++)
        (
          x: _unit(source[index].atNanoseconds - first, last - first),
          y: _unit(values[index] - values[low], span),
        ),
    ];
    final zero = values[low] <= BigInt.zero && values[high] >= BigInt.zero
        ? _unit(-values[low], span)
        : null;
    return TwrCheckpointPlot._(
      List.unmodifiable(points),
      source[low].cumulativeRate,
      source[high].cumulativeRate,
      zero,
    );
  }
  final List<({double x, double y})> points;
  final String minimum, maximum;
  final double? zeroY;
}

BigInt _scaled(String rate) {
  final negative = rate.startsWith('-');
  final parts = (negative ? rate.substring(1) : rate).split('.');
  final value = BigInt.parse(
    parts[0] + (parts.length == 1 ? '' : parts[1]).padRight(12, '0'),
  );
  return negative ? -value : value;
}

double _unit(BigInt offset, BigInt span) {
  if (span == BigInt.zero) {
    return 0.5;
  }
  // Convert only a bounded unit fraction; a valid prefix itself may exceed double.
  return ((offset * BigInt.from(1000000000)) ~/ span).toInt() / 1000000000;
}
