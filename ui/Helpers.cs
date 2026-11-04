using System;
using System.Globalization;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Data;
using System.Windows.Media;
using Wpf.Ui.Controls;

namespace NoMoreDPI;

public static class Dialogs
{
    public static async Task<bool> ConfirmAsync(string title, string text, string primary)
    {
        var box = new Wpf.Ui.Controls.MessageBox
        {
            Title = title,
            Content = text,
            PrimaryButtonText = primary,
            PrimaryButtonAppearance = ControlAppearance.Danger,
            CloseButtonText = "Cancel",
        };
        return await box.ShowDialogAsync() == Wpf.Ui.Controls.MessageBoxResult.Primary;
    }

    public static void Error(string text) => _ = new Wpf.Ui.Controls.MessageBox
    {
        Title = "No More DPI",
        Content = text,
        CloseButtonText = "OK",
    }.ShowDialogAsync();
}

public class StateBrushConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, CultureInfo culture) =>
        new SolidColorBrush(Icons.For(value is StateKind k ? k : StateKind.Off));

    public object ConvertBack(object value, Type targetType, object parameter, CultureInfo culture) => throw new NotSupportedException();
}

/// <summary>true → Visible; "invert" as the parameter flips it. Non-bool values count as
/// "is set" (not null, not empty string, not zero). Bound to a bool property it yields a bool.</summary>
public class VisibleConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, CultureInfo culture)
    {
        bool on = value switch
        {
            bool b => b,
            string s => s.Length > 0,
            int i => i != 0,
            null => false,
            _ => true,
        };
        if (parameter as string == "invert") on = !on;
        if (targetType == typeof(bool)) return on;
        return on ? Visibility.Visible : Visibility.Collapsed;
    }

    public object ConvertBack(object value, Type targetType, object parameter, CultureInfo culture) => throw new NotSupportedException();
}

public class NotConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, CultureInfo culture) => !(value is bool b && b);
    public object ConvertBack(object value, Type targetType, object parameter, CultureInfo culture) => !(value is bool b && b);
}
